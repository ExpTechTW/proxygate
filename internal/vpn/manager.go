package vpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/model"
	"github.com/ExpTechTW/proxygate/internal/store"
)

type Manager struct {
	ctx     context.Context
	config  *config.Store
	store   *store.Store
	logger  *log.Logger
	drivers map[string]Driver

	asyncMu        sync.Mutex
	async          sync.WaitGroup
	closing        bool
	switchMu       sync.Mutex
	autoMu         sync.Mutex
	failoverMu     sync.Mutex
	mu             sync.RWMutex
	session        Session
	node           model.Node
	protocol       string
	selection      string
	manualIP       string
	manualProtocol string
	state          string
	lastErr        error

	connectingIP   string
	cancelConnect  context.CancelFunc
	connectAttempt uint64
	generation     uint64
	lastHealth     time.Time
}

func NewManager(ctx context.Context, configStore *config.Store, database *store.Store, logger *log.Logger, drivers []Driver) *Manager {
	indexed := make(map[string]Driver, len(drivers))
	for _, driver := range drivers {
		indexed[driver.Protocol()] = driver
	}
	manager := &Manager{ctx: ctx, config: configStore, store: database, logger: logger, drivers: indexed, selection: "auto", state: "idle"}
	if mode, err := database.Metadata(ctx, "selection"); err == nil && mode == "manual" {
		if ip, ipErr := database.Metadata(ctx, "manual_ip"); ipErr == nil {
			if protocol, protocolErr := database.Metadata(ctx, "manual_protocol"); protocolErr == nil {
				manager.selection, manager.manualIP, manager.manualProtocol = "manual", ip, protocol
			}
		}
	}
	return manager
}

func (m *Manager) Start(ctx context.Context) error {
	m.mu.RLock()
	manualIP := m.manualIP
	manualProtocol := m.manualProtocol
	selection := m.selection
	m.mu.RUnlock()
	if selection == "manual" && manualIP != "" {
		if node, err := m.store.Node(ctx, manualIP); err == nil {
			m.setState("connecting", nil)
			if err := m.activate(ctx, node, "manual", true, 0, manualProtocol); err == nil {
				return nil
			}
		}
		m.setAutomatic(ctx)
	}
	return m.SwitchAutomatic(ctx)
}

func (m *Manager) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	m.mu.RLock()
	session := m.session
	m.mu.RUnlock()
	if session == nil {
		return nil, errors.New("no VPN session is active")
	}
	if strings.HasPrefix(network, "tcp") {
		return dialSessionIPv4(ctx, session, address, m.config.Get().DNSServers)
	}
	return session.DialContext(ctx, network, address)
}

func (m *Manager) ListenPacket(network, address string) (net.PacketConn, error) {
	m.mu.RLock()
	session := m.session
	m.mu.RUnlock()
	if session == nil {
		return nil, errors.New("no VPN session is active")
	}
	packetSession, ok := session.(packetSession)
	if !ok {
		return nil, errors.New("active VPN session does not support datagrams")
	}
	return packetSession.ListenPacket(network, address)
}

func (m *Manager) ResolveIPv4(ctx context.Context, host string) ([]net.IP, error) {
	m.mu.RLock()
	session := m.session
	m.mu.RUnlock()
	if session == nil {
		return nil, errors.New("no VPN session is active")
	}
	return resolveSessionIPv4(ctx, session, host, m.config.Get().DNSServers)
}

func (m *Manager) ActiveIP() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.session == nil {
		return ""
	}
	return m.node.IP
}

func dialSessionIPv4(ctx context.Context, session Session, address string, dnsServers []string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil {
			return nil, errors.New("the VPN probe target has no IPv4 address")
		}
		return session.DialContext(ctx, "tcp4", address)
	}
	addresses, err := resolveSessionIPv4(ctx, session, host, dnsServers)
	if err != nil {
		return nil, fmt.Errorf("resolve IPv4 address for %s through VPN: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("resolve IPv4 address for %s through VPN: no A record", host)
	}
	return session.DialContext(ctx, "tcp4", net.JoinHostPort(addresses[0].String(), port))
}

func (m *Manager) SelectManual(ctx context.Context, ip, protocol string) error {
	node, err := m.store.Node(ctx, ip)
	if err != nil {
		return err
	}
	return m.startCandidate(node, "manual", strings.TrimSpace(protocol))
}

func (m *Manager) ReconnectCurrent(ctx context.Context) error {
	m.mu.RLock()
	hasSession := m.session != nil
	ip, protocol, selection := m.node.IP, m.protocol, m.selection
	m.mu.RUnlock()
	if !hasSession || ip == "" || protocol == "" {
		return errors.New("no active VPN node to reconnect")
	}
	node, err := m.store.Node(ctx, ip)
	if err != nil {
		return err
	}
	return m.startCandidate(node, selection, protocol)
}

func (m *Manager) startCandidate(node model.Node, selection, forcedProtocol string) error {
	if err := m.validateForcedProtocol(node, forcedProtocol); err != nil {
		return err
	}
	m.mu.Lock()
	if m.cancelConnect != nil {
		m.cancelConnect()
	}
	connectCtx, cancel := context.WithCancel(m.ctx)
	m.cancelConnect = cancel
	m.connectAttempt++
	attempt := m.connectAttempt
	m.connectingIP = node.IP
	m.state = "connecting"
	m.lastErr = nil
	m.mu.Unlock()
	if !m.runAsync(func() {
		defer cancel()
		m.autoMu.Lock()
		defer m.autoMu.Unlock()
		if !m.isConnectAttempt(attempt) {
			return
		}
		err := m.activate(connectCtx, node, selection, true, attempt, forcedProtocol)
		if !m.finishConnectAttempt(attempt, err) {
			return
		}
		if err != nil {
			m.logger.Printf("[vpn] candidate connect to %s protocol=%s selection=%s: %v", node.IP, forcedProtocol, selection, err)
		}
	}) {
		cancel()
		m.finishConnectAttempt(attempt, errors.New("VPN manager is shutting down"))
		return errors.New("VPN manager is shutting down")
	}
	return nil
}

func (m *Manager) validateForcedProtocol(node model.Node, protocol string) error {
	if protocol == "" {
		return nil
	}
	if !slices.Contains(node.Protocols, protocol) {
		return fmt.Errorf("node %s does not support protocol %s", node.IP, protocol)
	}
	if _, ok := m.drivers[protocol]; !ok {
		return fmt.Errorf("protocol %s driver is unavailable", protocol)
	}
	return nil
}

func (m *Manager) SwitchAutomatic(ctx context.Context) error {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	return m.switchAutomatic(ctx)
}

func (m *Manager) switchAutomatic(ctx context.Context) error {
	settings := m.config.Get()
	nodes, err := m.store.Nodes(ctx, settings.SelectionMode, 10, 0)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return errors.New("no filtered VPN nodes are available")
	}
	var failures []error
	for _, node := range nodes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if activateErr := m.activate(ctx, node, "auto", false, 0, ""); activateErr == nil {
			return nil
		} else {
			failures = append(failures, fmt.Errorf("%s: %w", node.IP, activateErr))
			_ = m.store.MarkFailure(ctx, node.IP, activateErr.Error())
		}
	}
	err = fmt.Errorf("%s", strings.Join(func() []string {
		s := make([]string, len(failures))
		for i, e := range failures {
			s[i] = e.Error()
		}
		return s
	}(), "; "))
	m.recordError(err)
	return fmt.Errorf("connect to ranked VPN nodes: %w", err)
}

func (m *Manager) activate(ctx context.Context, node model.Node, selection string, requireHealth bool, expectedAttempt uint64, forcedProtocol string) error {
	m.switchMu.Lock()
	defer m.switchMu.Unlock()
	if !requireHealth {
		m.setState("connecting", nil)
	}
	session, protocol, err := m.connectNode(ctx, node, requireHealth, forcedProtocol)
	if err != nil {
		if !requireHealth {
			m.setState("error", err)
		}
		return err
	}
	installed := false
	defer func() {
		if !installed {
			_ = session.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.install(session, node, protocol, selection, expectedAttempt, forcedProtocol) {
		return context.Canceled
	}
	installed = true
	if requireHealth {
		m.SetLastHealth(time.Now())
	}
	return nil
}

func (m *Manager) connectNode(ctx context.Context, node model.Node, requireHealth bool, forcedProtocol string) (Session, string, error) {
	if ip := net.ParseIP(node.IP); ip == nil || ip.To4() == nil {
		return nil, "", fmt.Errorf("node endpoint %q is not IPv4", node.IP)
	}
	settings := m.config.Get()
	protocols := settings.ProtocolPriority
	if forcedProtocol != "" {
		if err := m.validateForcedProtocol(node, forcedProtocol); err != nil {
			return nil, "", err
		}
		protocols = []string{forcedProtocol}
	}
	var failures []error
	connectTimeout, _ := settings.ConnectTimeoutDuration()
	for _, protocol := range protocols {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		if !slices.Contains(node.Protocols, protocol) {
			continue
		}
		driver, ok := m.drivers[protocol]
		if !ok {
			failures = append(failures, fmt.Errorf("%s driver is unavailable", protocol))
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		session, err := driver.Start(attemptCtx, node, settings, m.logger)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", protocol, err))
			continue
		}
		if requireHealth {
			if err := probeSessionHealth(ctx, session, settings); err != nil {
				_ = session.Close()
				failures = append(failures, fmt.Errorf("%s candidate health check: %w", protocol, err))
				continue
			}
		}
		return session, protocol, nil
	}
	if len(failures) == 0 {
		return nil, "", errors.New("node has no enabled protocol with an available driver")
	}
	err := errors.New(strings.Join(func() []string {
		s := make([]string, len(failures))
		for i, e := range failures {
			s[i] = e.Error()
		}
		return s
	}(), "; "))
	return nil, "", err
}

func (m *Manager) TestNode(ctx context.Context, ip string) (speed int64, resultErr error) {
	defer func() {
		if resultErr == nil || ctx.Err() != nil {
			return
		}
		if err := m.store.SaveMeasurementFailure(ctx, ip); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("save speed test failure: %w", err))
		}
	}()
	node, err := m.store.Node(ctx, ip)
	if err != nil {
		return 0, err
	}
	session, _, err := m.connectNode(ctx, node, false, "")
	if err != nil {
		return 0, err
	}
	defer session.Close()
	settings := m.config.Get()
	testTimeout, _ := settings.SpeedTestTimeoutDuration()
	testCtx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialSessionIPv4(ctx, session, address, settings.DNSServers)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(testCtx, http.MethodGet, settings.SpeedTestURL, nil)
	if err != nil {
		return 0, err
	}
	started := time.Now()
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return 0, fmt.Errorf("speed test download: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("speed test download returned %s", response.Status)
	}
	bytesRead, copyErr := io.Copy(io.Discard, io.LimitReader(response.Body, 10_000_001))
	timedOut := copyErr != nil && errors.Is(testCtx.Err(), context.DeadlineExceeded)
	if copyErr != nil && !timedOut {
		return 0, copyErr
	}
	elapsed := time.Since(started)
	if elapsed <= 0 || bytesRead == 0 {
		if timedOut {
			return 0, errors.New("speed test timed out before receiving data")
		}
		return 0, errors.New("speed test returned no data")
	}
	bitsPerSecond := int64(float64(bytesRead*8) / elapsed.Seconds())
	if err := m.store.SaveMeasurement(ctx, ip, bitsPerSecond); err != nil {
		return 0, err
	}
	if timedOut {
		m.logger.Printf("[vpn] speed test node=%s reached timeout=%s bytes=%d average=%d bps", ip, settings.SpeedTestTimeout, bytesRead, bitsPerSecond)
	}
	return bitsPerSecond, nil
}

func (m *Manager) install(session Session, node model.Node, protocol, selection string, expectedAttempt uint64, forcedProtocol string) bool {
	m.mu.Lock()
	if expectedAttempt != 0 && m.connectAttempt != expectedAttempt {
		m.mu.Unlock()
		return false
	}
	old := m.session
	oldIP := m.node.IP
	m.session, m.node, m.protocol, m.selection = session, node, protocol, selection
	if selection == "manual" {
		m.manualIP = node.IP
		m.manualProtocol = forcedProtocol
	} else {
		m.manualIP = ""
		m.manualProtocol = ""
	}
	m.state, m.lastErr = "connected", nil
	if oldIP != "" && oldIP != node.IP {
		if err := m.store.DeleteNodeIfStale(m.ctx, oldIP); err != nil {
			m.logger.Printf("[vpn] remove stale node=%s: %v", oldIP, err)
		}
	}
	m.generation++
	generation := m.generation
	manualIP := m.manualIP
	manualProtocol := m.manualProtocol
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	_ = m.store.SetMetadata(m.ctx, "selection", selection)
	_ = m.store.SetMetadata(m.ctx, "manual_ip", manualIP)
	_ = m.store.SetMetadata(m.ctx, "manual_protocol", manualProtocol)
	m.logger.Printf("[vpn] active node=%s country=%s protocol=%s selection=%s", node.IP, node.CountryShort, protocol, selection)
	m.runAsync(func() {
		err := session.Wait(m.ctx)
		if m.ctx.Err() == nil && m.isGeneration(generation) {
			if err == nil {
				err = errors.New("VPN session ended")
			} else {
				err = fmt.Errorf("VPN session ended: %w", err)
			}
			m.Failover(m.ctx, err)
		}
	})
	return true
}

func (m *Manager) isConnectAttempt(attempt uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connectAttempt == attempt
}

func (m *Manager) finishConnectAttempt(attempt uint64, err error) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.connectAttempt != attempt {
		return false
	}
	m.connectingIP = ""
	m.cancelConnect = nil
	if err != nil {
		if m.session != nil {
			m.state = "connected"
		} else {
			m.state = "error"
		}
		m.lastErr = err
	}
	return true
}

func (m *Manager) Failover(ctx context.Context, cause error) {
	m.failover(ctx, nil, cause)
}

func (m *Manager) FailoverSession(ctx context.Context, expected Session, cause error) {
	m.failover(ctx, expected, cause)
}

func (m *Manager) failover(ctx context.Context, expected Session, cause error) {
	m.failoverMu.Lock()
	defer m.failoverMu.Unlock()
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	m.mu.Lock()
	if expected != nil && m.session != expected {
		m.mu.Unlock()
		return
	}
	failedIP := m.node.IP
	m.selection, m.manualIP, m.manualProtocol = "auto", "", ""
	m.lastErr = cause
	m.mu.Unlock()
	if failedIP != "" {
		_ = m.store.MarkFailure(ctx, failedIP, cause.Error())
	}
	_ = m.store.SetMetadata(ctx, "selection", "auto")
	_ = m.store.SetMetadata(ctx, "manual_ip", "")
	_ = m.store.SetMetadata(ctx, "manual_protocol", "")
	m.logger.Printf("[vpn] failover: node=%s reason=%v", failedIP, cause)
	if err := m.switchAutomatic(ctx); err != nil {
		m.logger.Printf("[vpn] failover failed: %v", err)
	}
}

func (m *Manager) AfterRefresh(ctx context.Context) {
	m.mu.RLock()
	selection, activeIP := m.selection, m.node.IP
	hasSession := m.session != nil
	m.mu.RUnlock()
	if !hasSession {
		m.runAsync(func() {
			if err := m.SwitchAutomatic(m.ctx); err != nil {
				m.logger.Printf("[vpn] connect after refresh: %v", err)
			}
		})
		return
	}
	settings := m.config.Get()
	if !settings.FollowRankingOnRefresh {
		return
	}
	if selection == "auto" {
		m.runAsync(func() {
			if err := m.SwitchAutomatic(ctx); err != nil {
				m.logger.Printf("[vpn] follow refreshed ranking: %v", err)
			}
		})
		return
	}
	if activeIP != "" {
		if _, err := m.store.Node(ctx, activeIP); store.IsNotFound(err) {
			m.runAsync(func() { m.Failover(m.ctx, errors.New("active node disappeared after refresh")) })
		}
	}
}

func (m *Manager) Session() Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.session
}

func (m *Manager) Status(refreshRunning bool, lastRefresh time.Time, refreshErr error) model.RuntimeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := model.RuntimeStatus{State: m.state, Selection: m.selection, ActiveIP: m.node.IP, ActiveHostName: m.node.HostName, ActiveProtocol: m.protocol, ConnectingIP: m.connectingIP, LastRefresh: lastRefresh, LastHealthCheck: m.lastHealth, RefreshRunning: refreshRunning}
	if m.lastErr != nil {
		status.LastError = m.lastErr.Error()
	} else if refreshErr != nil {
		status.LastError = refreshErr.Error()
	}
	return status
}

func (m *Manager) SetLastHealth(value time.Time) {
	m.mu.Lock()
	m.lastHealth = value
	m.mu.Unlock()
}

func (m *Manager) Close() error {
	m.asyncMu.Lock()
	m.closing = true
	m.asyncMu.Unlock()

	m.mu.Lock()
	session := m.session
	m.session = nil
	m.generation++
	if m.cancelConnect != nil {
		m.cancelConnect()
		m.cancelConnect = nil
	}
	m.connectAttempt++
	m.connectingIP = ""
	m.mu.Unlock()
	var closeErrors []error
	if session != nil {
		closeErrors = append(closeErrors, session.Close())
	}
	m.async.Wait()
	m.mu.Lock()
	lateSession := m.session
	m.session = nil
	m.generation++
	m.mu.Unlock()
	if lateSession != nil && lateSession != session {
		closeErrors = append(closeErrors, lateSession.Close())
	}
	return errors.Join(closeErrors...)
}

func (m *Manager) runAsync(fn func()) bool {
	m.asyncMu.Lock()
	defer m.asyncMu.Unlock()
	if m.closing {
		return false
	}
	m.async.Add(1)
	go func() {
		defer m.async.Done()
		fn()
	}()
	return true
}

func (m *Manager) isGeneration(value uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.generation == value
}

func (m *Manager) setAutomatic(ctx context.Context) {
	m.mu.Lock()
	m.selection, m.manualIP, m.manualProtocol = "auto", "", ""
	m.mu.Unlock()
	_ = m.store.SetMetadata(ctx, "selection", "auto")
	_ = m.store.SetMetadata(ctx, "manual_ip", "")
	_ = m.store.SetMetadata(ctx, "manual_protocol", "")
}

func (m *Manager) setState(state string, err error) {
	m.mu.Lock()
	m.state, m.lastErr = state, err
	m.mu.Unlock()
}

func (m *Manager) recordError(err error) { m.setState("error", err) }
