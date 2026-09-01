package vpngate

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/ExpTechTW/proxygate/internal/model"
)

const maxResponseSize = 32 << 20

type Client struct{ httpClient *http.Client }

func NewClient() *Client {
	return &Client{httpClient: &http.Client{Timeout: 45 * time.Second}}
}

func (c *Client) Fetch(ctx context.Context, sourceURL, expression string) ([]model.Node, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "github.com/ExpTechTW/proxygate/1.0")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download VPN Gate CSV: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download VPN Gate CSV: unexpected HTTP status %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read VPN Gate CSV: %w", err)
	}
	if len(data) > maxResponseSize {
		return nil, errors.New("VPN Gate CSV exceeds 32 MiB")
	}
	nodes, err := ParseCSV(string(data), time.Now())
	if err != nil {
		return nil, err
	}
	filter, err := CompileFilter(expression)
	if err != nil {
		return nil, err
	}
	filtered := make([]model.Node, 0, len(nodes))
	for _, node := range nodes {
		keep, err := filter.Match(node)
		if err != nil {
			return nil, fmt.Errorf("filter node %s: %w", node.IP, err)
		}
		if keep {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func ParseCSV(data string, refreshedAt time.Time) ([]model.Node, error) {
	headerOffset := strings.Index(data, "#HostName,")
	if headerOffset < 0 {
		headerOffset = strings.Index(data, "HostName,")
	}
	if headerOffset < 0 || headerOffset >= len(data) {
		return nil, errors.New("VPN Gate CSV header was not found")
	}
	payload := data[headerOffset:]
	if strings.HasPrefix(payload, "#") {
		payload = payload[1:]
	}
	reader := csv.NewReader(strings.NewReader(payload))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	if _, err := reader.Read(); err != nil {
		return nil, fmt.Errorf("read VPN Gate CSV header: %w", err)
	}
	unique := make(map[string]model.Node)
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read VPN Gate CSV row: %w", err)
		}
		if len(record) < 14 || strings.HasPrefix(record[0], "*") {
			continue
		}
		node, err := parseRecord(record, refreshedAt)
		if err != nil {
			continue
		}
		// A node only needs one recognized protocol. Do not require it to
		// implement the complete configured protocol priority list.
		if len(node.Protocols) == 0 {
			continue
		}
		if current, exists := unique[node.IP]; !exists || node.Score > current.Score {
			unique[node.IP] = node
		}
	}
	result := make([]model.Node, 0, len(unique))
	for _, node := range unique {
		result = append(result, node)
	}
	if len(result) == 0 {
		return nil, errors.New("VPN Gate CSV contains no valid nodes")
	}
	return result, nil
}

func parseRecord(record []string, refreshedAt time.Time) (model.Node, error) {
	ip := net.ParseIP(strings.TrimSpace(record[1]))
	if ip == nil || ip.To4() == nil {
		return model.Node{}, errors.New("invalid IPv4 address")
	}
	openVPNConfig := ""
	if len(record) > 14 {
		openVPNConfig = decodeOpenVPNConfig(record[14])
	}
	node := model.Node{
		HostName: strings.TrimSpace(record[0]), IP: strings.TrimSpace(record[1]),
		Port:  detectPort(openVPNConfig),
		Score: parseInteger(record[2]), PingMS: parseInteger(record[3]), SpeedBPS: parseInteger(record[4]),
		CountryLong: record[5], CountryShort: record[6], Sessions: parseInteger(record[7]),
		UptimeMS: parseInteger(record[8]), TotalUsers: parseInteger(record[9]), TotalTraffic: parseInteger(record[10]),
		LogType: record[11], Operator: record[12], Message: record[13], OpenVPNConfig: openVPNConfig, RefreshedAt: refreshedAt,
	}
	node.Protocols = detectProtocols(node.OpenVPNConfig)
	return node, nil
}

// OpenVPN is an optional node capability. A missing or malformed profile must
// not hide a node that can still be reached through another supported driver.
func decodeOpenVPNConfig(value string) string {
	encoded := strings.TrimSpace(value)
	if encoded == "" {
		return ""
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoded, err := encoding.DecodeString(encoded)
		if err == nil {
			return string(decoded)
		}
	}
	return ""
}

func parseInteger(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func detectPort(openVPNConfig string) int {
	port := 443
	for _, line := range strings.Split(openVPNConfig, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		if fields[0] == "port" && len(fields) >= 2 {
			if p, err := strconv.Atoi(fields[1]); err == nil && p > 0 && p <= 65535 {
				return p
			}
		}
		if fields[0] == "remote" && len(fields) >= 3 {
			if p, err := strconv.Atoi(fields[2]); err == nil && p > 0 && p <= 65535 {
				port = p
			}
		}
	}
	return port
}

func detectProtocols(openVPNConfig string) []string {
	protocols := []string{model.ProtocolSoftEtherTLS, model.ProtocolSSTP, model.ProtocolL2TP}
	seenUDP, seenTCP := false, false
	for _, line := range strings.Split(openVPNConfig, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || (fields[0] != "proto" && fields[0] != "remote") {
			continue
		}
		candidate := fields[1]
		if fields[0] == "remote" && len(fields) >= 4 {
			candidate = fields[3]
		}
		candidate = strings.ToLower(candidate)
		seenUDP = seenUDP || strings.HasPrefix(candidate, "udp")
		seenTCP = seenTCP || strings.HasPrefix(candidate, "tcp")
	}
	if seenUDP {
		protocols = append([]string{model.ProtocolOpenVPNUDP}, protocols...)
	}
	if seenTCP {
		protocols = append(protocols, model.ProtocolOpenVPNTCP)
	}
	return protocols
}

type Filter struct {
	vm       *goja.Runtime
	function goja.Callable
}

func CompileFilter(expression string) (*Filter, error) {
	if strings.TrimSpace(expression) == "" {
		expression = "true"
	}
	vm := goja.New()
	if err := vm.Set("cidrContains", func(cidr, ip string) bool {
		prefix, prefixErr := netip.ParsePrefix(cidr)
		address, addressErr := netip.ParseAddr(ip)
		return prefixErr == nil && addressErr == nil && prefix.Contains(address)
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("includesIgnoreCase", func(value, search string) bool {
		return strings.Contains(strings.ToLower(value), strings.ToLower(search))
	}); err != nil {
		return nil, err
	}
	program, err := goja.Compile("filter.js", "(function(node) { 'use strict'; return Boolean("+expression+"); })", false)
	if err != nil {
		return nil, fmt.Errorf("compile filter expression: %w", err)
	}
	value, err := vm.RunProgram(program)
	if err != nil {
		return nil, fmt.Errorf("initialize filter expression: %w", err)
	}
	function, ok := goja.AssertFunction(value)
	if !ok {
		return nil, errors.New("filter expression did not create a function")
	}
	return &Filter{vm: vm, function: function}, nil
}

func (f *Filter) Match(node model.Node) (bool, error) {
	object := map[string]any{
		"hostName": node.HostName, "ip": node.IP, "score": node.Score, "pingMs": node.PingMS,
		"speedBps": node.SpeedBPS, "countryLong": node.CountryLong, "countryShort": node.CountryShort,
		"sessions": node.Sessions, "uptimeMs": node.UptimeMS, "totalUsers": node.TotalUsers,
		"totalTraffic": node.TotalTraffic, "logType": node.LogType, "operator": node.Operator,
		"operatorMessage": node.Message, "protocols": node.Protocols,
	}
	interrupted := make(chan struct{})
	timer := time.AfterFunc(100*time.Millisecond, func() {
		f.vm.Interrupt("filter expression exceeded 100ms")
		close(interrupted)
	})
	value, err := f.function(goja.Undefined(), f.vm.ToValue(object))
	if !timer.Stop() {
		<-interrupted
	}
	f.vm.ClearInterrupt()
	if err != nil {
		return false, err
	}
	return value.ToBoolean(), nil
}
