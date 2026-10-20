package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
)

type blockedDNSDialer struct {
	upstream net.PacketConn
	started  chan struct{}
}

func (d *blockedDNSDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("unexpected TCP dial")
}

func (d *blockedDNSDialer) ListenPacket(network, _ string) (net.PacketConn, error) {
	if network != "udp4" {
		return nil, errors.New("IPv6 is unavailable")
	}
	return d.upstream, nil
}

func (d *blockedDNSDialer) ResolveIPv4(ctx context.Context, _ string) ([]net.IP, error) {
	close(d.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestUDPAssociateReturnsPacketsWhileDNSIsBlocked(t *testing.T) {
	settings, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	dialer := &blockedDNSDialer{upstream: upstream, started: make(chan struct{})}
	server := New(settings, dialer, log.New(io.Discard, "", 0))
	client, proxy := tcpPair(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.handleUDPAssociate(ctx, proxy) }()
	defer func() {
		cancel()
		if err := relayResult(t, done); err != nil {
			t.Error(err)
		}
	}()
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if reply[1] != replyOK || reply[3] != addressIPv4 {
		t.Fatalf("unexpected UDP ASSOCIATE reply: %x", reply)
	}
	relay := &net.UDPAddr{IP: net.IP(reply[4:8]), Port: int(binary.BigEndian.Uint16(reply[8:]))}
	packets, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packets.Close()
	_ = packets.SetDeadline(time.Now().Add(time.Second))
	host := "blocked.example"
	request := append([]byte{0, 0, 0, addressDomain, byte(len(host))}, host...)
	request = binary.BigEndian.AppendUint16(request, 443)
	if _, err := packets.WriteTo(request, relay); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialer.started:
	case <-time.After(time.Second):
		t.Fatal("DNS lookup did not start")
	}
	// Simulate return traffic from an existing UDP flow during the lookup.
	if _, err := packets.WriteTo([]byte("return traffic"), upstream.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1024)
	n, _, err := packets.ReadFrom(buffer)
	if err != nil {
		t.Fatalf("return traffic was blocked by DNS: %v", err)
	}
	_, _, payload, err := parseUDPRequest(buffer[:n])
	if err != nil || string(payload) != "return traffic" {
		t.Fatalf("response = %q, error = %v", payload, err)
	}
}
