package socks5

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	return client, server
}

func relayResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("relay did not release its copy goroutines")
		return nil
	}
}

func TestRelayTCPPreservesHalfClosedResponse(t *testing.T) {
	client, proxy := tcpPair(t)
	upstream, target := tcpPair(t)
	done := make(chan error, 1)
	go func() { done <- relayTCP(t.Context(), proxy, upstream, time.Minute, time.Second) }()
	if _, err := io.WriteString(client, "request"); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	request, err := io.ReadAll(target)
	if err != nil || string(request) != "request" {
		t.Fatalf("request = %q, error = %v", request, err)
	}
	if _, err := io.WriteString(target, "response after EOF"); err != nil {
		t.Fatal(err)
	}
	if err := target.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(client)
	if err != nil || string(response) != "response after EOF" {
		t.Fatalf("response = %q, error = %v", response, err)
	}
	if err := relayResult(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestRelayTCPStopsOnCancellation(t *testing.T) {
	client, proxy := net.Pipe()
	upstream, target := net.Pipe()
	defer client.Close()
	defer target.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- relayTCP(ctx, proxy, upstream, time.Hour, time.Hour) }()
	cancel()
	if err := relayResult(t, done); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := target.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("upstream was not closed: %v", err)
	}
}

func TestRelayTCPIdleTimeout(t *testing.T) {
	client, proxy := net.Pipe()
	upstream, target := net.Pipe()
	defer client.Close()
	defer target.Close()
	done := make(chan error, 1)
	go func() { done <- relayTCP(t.Context(), proxy, upstream, 20*time.Millisecond, time.Hour) }()
	if err := relayResult(t, done); err == nil || !strings.Contains(err.Error(), "idle timeout") {
		t.Fatalf("error = %v", err)
	}
}

type failedReadConn struct {
	net.Conn
	err error
}

func (c failedReadConn) Read([]byte) (int, error) { return 0, c.err }

func TestRelayTCPErrorClosesOtherDirection(t *testing.T) {
	client, proxy := net.Pipe()
	upstream, target := net.Pipe()
	defer client.Close()
	defer target.Close()
	want := errors.New("upstream read failed")
	done := make(chan error, 1)
	go func() {
		done <- relayTCP(t.Context(), proxy, failedReadConn{Conn: upstream, err: want}, time.Hour, time.Hour)
	}()
	if err := relayResult(t, done); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestRelayTCPHalfCloseTimeout(t *testing.T) {
	client, proxy := tcpPair(t)
	upstream, target := tcpPair(t)
	done := make(chan error, 1)
	go func() { done <- relayTCP(t.Context(), proxy, upstream, time.Hour, 20*time.Millisecond) }()
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(target); err != nil {
		t.Fatal(err)
	}
	if err := relayResult(t, done); err == nil || !strings.Contains(err.Error(), "half-close timeout") {
		t.Fatalf("error = %v", err)
	}
}
