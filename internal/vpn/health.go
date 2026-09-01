package vpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
)

type HealthChecker struct {
	config  *config.Store
	manager *Manager
}

func NewHealthChecker(configStore *config.Store, manager *Manager) *HealthChecker {
	return &HealthChecker{config: configStore, manager: manager}
}

func (h *HealthChecker) Check(ctx context.Context) error {
	return h.check(ctx, h.manager.Session())
}

func (h *HealthChecker) CheckSession(ctx context.Context, session Session) error {
	return h.check(ctx, session)
}

func (h *HealthChecker) check(ctx context.Context, session Session) error {
	settings := h.config.Get()
	err := probeSessionHealth(ctx, session, settings)
	h.manager.SetLastHealth(time.Now())
	return err
}

func probeSessionHealth(ctx context.Context, session Session, settings config.Config) error {
	timeout, _ := settings.MonitorTimeout()
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if session == nil {
		return errors.New("health check: no active VPN session")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialSessionIPv4(ctx, session, address, settings.DNSServers)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(checkCtx, http.MethodGet, settings.Monitor.URL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("health check request: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("health check expected HTTP 204, got %s", response.Status)
	}
	return nil
}
