package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ExpTechTW/proxygate/internal/model"
)

const (
	DefaultSourceURL    = "http://www.vpngate.net/api/iphone/"
	DefaultSpeedTestURL = "http://storage.googleapis.com/gcp-public-data-landsat/LC08/01/001/002/LC08_L1GT_001002_20160817_20170322_01_T2/LC08_L1GT_001002_20160817_20170322_01_T2_B1.TIF"
)

var defaultDNSServers = []string{"1.1.1.1:53", "8.8.8.8:53"}

type Config struct {
	SourceURL              string   `json:"sourceUrl"`
	RefreshInterval        string   `json:"refreshInterval"`
	FilterExpression       string   `json:"filterExpression"`
	SelectionMode          string   `json:"selectionMode"`
	FollowRankingOnRefresh bool     `json:"followRankingOnRefresh"`
	SOCKS5                 SOCKS5   `json:"socks5"`
	Monitor                Monitor  `json:"monitor"`
	Web                    Web      `json:"web"`
	DatabasePath           string   `json:"databasePath"`
	DNSServers             []string `json:"dnsServers"`
	SpeedTestURL           string   `json:"speedTestUrl"`
	SpeedTestTimeout       string   `json:"speedTestTimeout"`
	ProtocolPriority       []string `json:"protocolPriority"`
	ConnectTimeout         string   `json:"connectTimeout"`
	VPNGateUsername        string   `json:"vpnGateUsername"`
	VPNGatePassword        string   `json:"vpnGatePassword"`
	VPNGatePreSharedKey    string   `json:"vpnGatePreSharedKey"`
}

type SOCKS5 struct {
	ListenAddress string `json:"listenAddress"`
	Username      string `json:"username"`
	Password      string `json:"password"`
}

type Monitor struct {
	URL      string `json:"url"`
	Interval string `json:"interval"`
	Timeout  string `json:"timeout"`
}

type Web struct {
	ListenAddress string `json:"listenAddress"`
	Username      string `json:"username"`
	PasswordHash  string `json:"passwordHash"`
	SessionSecret string `json:"sessionSecret"`
}

func Default() Config {
	value, _ := defaultConfig()
	return value
}

func defaultConfig() (Config, string) {
	password := randomPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic(fmt.Sprintf("generate initial web password hash: %v", err))
	}
	return Config{
		SourceURL: DefaultSourceURL, RefreshInterval: "30m", FilterExpression: "true",
		SelectionMode: "speed", FollowRankingOnRefresh: true,
		SOCKS5:       SOCKS5{ListenAddress: "127.0.0.1:1080"},
		Monitor:      Monitor{URL: "https://www.google.com/generate_204", Interval: "30s", Timeout: "10s"},
		Web:          Web{ListenAddress: "127.0.0.1:8080", Username: "admin", PasswordHash: string(hash), SessionSecret: randomSecret()},
		DatabasePath: "data/proxygate.db", ConnectTimeout: "15s",
		DNSServers: append([]string(nil), defaultDNSServers...), SpeedTestURL: DefaultSpeedTestURL, SpeedTestTimeout: "45s",
		ProtocolPriority: []string{model.ProtocolOpenVPNUDP, model.ProtocolOpenVPNTCP, model.ProtocolSoftEtherTLS, model.ProtocolSSTP, model.ProtocolL2TP},
		VPNGateUsername:  "vpn", VPNGatePassword: "vpn", VPNGatePreSharedKey: "vpn",
	}, password
}

func (c Config) Validate() error {
	if parsed, err := url.ParseRequestURI(c.SourceURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("sourceUrl must be an HTTP or HTTPS URL")
	}
	if _, err := c.RefreshDuration(); err != nil {
		return fmt.Errorf("refreshInterval: %w", err)
	}
	if c.SelectionMode != "speed" && c.SelectionMode != "ping" && c.SelectionMode != "score" {
		return errors.New("selectionMode must be speed, ping, or score")
	}
	if err := validateAddress("socks5.listenAddress", c.SOCKS5.ListenAddress); err != nil {
		return err
	}
	if (c.SOCKS5.Username == "") != (c.SOCKS5.Password == "") {
		return errors.New("socks5 username and password must both be set or both be empty")
	}
	if len(c.SOCKS5.Username) > 255 || len(c.SOCKS5.Password) > 255 {
		return errors.New("socks5 username and password must not exceed 255 bytes")
	}
	if err := validateAddress("web.listenAddress", c.Web.ListenAddress); err != nil {
		return err
	}
	if c.Web.Username == "" || c.Web.PasswordHash == "" || c.Web.SessionSecret == "" {
		return errors.New("web username, passwordHash, and sessionSecret are required")
	}
	if parsed, err := url.ParseRequestURI(c.Monitor.URL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("monitor.url must be an HTTP or HTTPS URL")
	}
	if _, err := c.MonitorInterval(); err != nil {
		return fmt.Errorf("monitor.interval: %w", err)
	}
	if _, err := c.MonitorTimeout(); err != nil {
		return fmt.Errorf("monitor.timeout: %w", err)
	}
	if _, err := c.ConnectTimeoutDuration(); err != nil {
		return fmt.Errorf("connectTimeout: %w", err)
	}
	if strings.TrimSpace(c.DatabasePath) == "" {
		return errors.New("databasePath is required")
	}
	if len(c.DNSServers) == 0 {
		return errors.New("dnsServers must not be empty")
	}
	seenDNS := make(map[string]bool, len(c.DNSServers))
	for _, server := range c.DNSServers {
		host, portText, err := net.SplitHostPort(server)
		if err != nil {
			return fmt.Errorf("dnsServers contains invalid address %q: %w", server, err)
		}
		if ip := net.ParseIP(host); ip == nil || ip.To4() == nil {
			return fmt.Errorf("dnsServers entry %q must use a literal IPv4 address", server)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("dnsServers entry %q has an invalid port", server)
		}
		if seenDNS[server] {
			return fmt.Errorf("dnsServers contains duplicate address %q", server)
		}
		seenDNS[server] = true
	}
	if parsed, err := url.ParseRequestURI(c.SpeedTestURL); err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("speedTestUrl must be an HTTP or HTTPS URL")
	}
	if _, err := c.SpeedTestTimeoutDuration(); err != nil {
		return fmt.Errorf("speedTestTimeout: %w", err)
	}
	validProtocols := []string{model.ProtocolSoftEtherTLS, model.ProtocolOpenVPNTCP, model.ProtocolOpenVPNUDP, model.ProtocolSSTP, model.ProtocolL2TP}
	if len(c.ProtocolPriority) == 0 {
		return errors.New("protocolPriority must not be empty")
	}
	seen := make(map[string]bool)
	for _, protocol := range c.ProtocolPriority {
		if !slices.Contains(validProtocols, protocol) || seen[protocol] {
			return fmt.Errorf("invalid or duplicate protocol %q", protocol)
		}
		seen[protocol] = true
	}
	if c.VPNGateUsername == "" || c.VPNGatePassword == "" || c.VPNGatePreSharedKey == "" {
		return errors.New("VPN Gate username, password, and pre-shared key are required")
	}
	if strings.ContainsAny(c.VPNGateUsername, "\r\n") || strings.ContainsAny(c.VPNGatePassword, "\r\n") {
		return errors.New("VPN Gate username and password must not contain newlines")
	}
	return nil
}

func positiveDuration(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, errors.New("must be greater than zero")
	}
	return duration, nil
}

func (c Config) RefreshDuration() (time.Duration, error) { return positiveDuration(c.RefreshInterval) }
func (c Config) MonitorInterval() (time.Duration, error) { return positiveDuration(c.Monitor.Interval) }
func (c Config) MonitorTimeout() (time.Duration, error)  { return positiveDuration(c.Monitor.Timeout) }
func (c Config) SpeedTestTimeoutDuration() (time.Duration, error) {
	return positiveDuration(c.SpeedTestTimeout)
}
func (c Config) ConnectTimeoutDuration() (time.Duration, error) {
	return positiveDuration(c.ConnectTimeout)
}

func validateAddress(field, value string) error {
	if _, _, err := net.SplitHostPort(value); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

type Store struct {
	mu     sync.RWMutex
	path   string
	config Config
}

func Load(path string) (*Store, error) {
	return load(path, nil)
}

// LoadWithLogger behaves like Load and reports the generated first-run
// password exactly once, when a configuration file is created.
func LoadWithLogger(path string, logger *log.Logger) (*Store, error) {
	return load(path, logger)
}

func load(path string, logger *log.Logger) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var value Config
	initialPassword := ""
	data, readErr := os.ReadFile(absolute)
	if readErr == nil {
		if err := requireCurrentConfigFields(data); err != nil {
			return nil, fmt.Errorf("decode config: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode config: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, errors.New("decode config: trailing JSON data")
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		value, initialPassword = defaultConfig()
	} else {
		return nil, fmt.Errorf("read config: %w", readErr)
	}
	applyEnvironmentOverrides(&value)
	if !filepath.IsAbs(value.DatabasePath) {
		value.DatabasePath = filepath.Join(filepath.Dir(absolute), value.DatabasePath)
	}
	if err := value.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	store := &Store{path: absolute, config: value}
	if errors.Is(readErr, os.ErrNotExist) {
		if err := store.Save(value); err != nil {
			return nil, err
		}
		if logger != nil {
			logger.Printf("[config] generated first-run web password for %s: %s", value.Web.Username, initialPassword)
		}
	}
	return store, nil
}

func applyEnvironmentOverrides(value *Config) {
	if address := strings.TrimSpace(os.Getenv("PROXYGATE_WEB_LISTEN_ADDRESS")); address != "" {
		value.Web.ListenAddress = address
	}
	if address := strings.TrimSpace(os.Getenv("PROXYGATE_SOCKS5_LISTEN_ADDRESS")); address != "" {
		value.SOCKS5.ListenAddress = address
	}
	if path := strings.TrimSpace(os.Getenv("PROXYGATE_DATABASE_PATH")); path != "" {
		value.DatabasePath = path
	}
}

func requireCurrentConfigFields(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if err := requireJSONFields(fields, "config",
		"sourceUrl", "refreshInterval", "filterExpression", "selectionMode", "followRankingOnRefresh",
		"socks5", "monitor", "web", "databasePath", "dnsServers", "speedTestUrl", "speedTestTimeout",
		"protocolPriority", "connectTimeout", "vpnGateUsername", "vpnGatePassword", "vpnGatePreSharedKey"); err != nil {
		return err
	}
	for name, required := range map[string][]string{
		"socks5":  {"listenAddress", "username", "password"},
		"monitor": {"url", "interval", "timeout"},
		"web":     {"listenAddress", "username", "passwordHash", "sessionSecret"},
	} {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(fields[name], &nested); err != nil {
			return fmt.Errorf("%s must be an object", name)
		}
		if err := requireJSONFields(nested, name, required...); err != nil {
			return err
		}
	}
	return nil
}

func requireJSONFields(fields map[string]json.RawMessage, object string, required ...string) error {
	for _, name := range required {
		if _, exists := fields[name]; !exists {
			return fmt.Errorf("%s is missing required field %q", object, name)
		}
	}
	return nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value := s.config
	value.ProtocolPriority = append([]string(nil), value.ProtocolPriority...)
	value.DNSServers = append([]string(nil), value.DNSServers...)
	return value
}

func (s *Store) Save(value Config) error {
	if !filepath.IsAbs(value.DatabasePath) {
		value.DatabasePath = filepath.Join(filepath.Dir(s.path), value.DatabasePath)
	}
	if err := value.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	encodedValue := value
	relative, err := filepath.Rel(filepath.Dir(s.path), value.DatabasePath)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		encodedValue.DatabasePath = relative
	}
	data, err := json.MarshalIndent(encodedValue, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	s.mu.Lock()
	s.config = value
	s.mu.Unlock()
	return nil
}

func randomSecret() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

func randomPassword() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(18))
}

func randomBytes(size int) []byte {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		panic(fmt.Sprintf("read cryptographic randomness: %v", err))
	}
	return data
}
