package vpn

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type dnsTestSession struct {
	dial func(context.Context, string, string) (net.Conn, error)
}

func (s *dnsTestSession) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return s.dial(ctx, network, address)
}

func (*dnsTestSession) Wait(context.Context) error { return nil }
func (*dnsTestSession) Close() error               { return nil }

func dnsTestConnection(t *testing.T, ttl uint32, release <-chan struct{}) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go func() {
		defer server.Close()
		payload := make([]byte, 1024)
		n, err := server.Read(payload)
		if err != nil {
			return
		}
		var query dnsmessage.Message
		if err := query.Unpack(payload[:n]); err != nil || len(query.Questions) != 1 {
			t.Errorf("invalid DNS query: %v", err)
			return
		}
		if release != nil {
			select {
			case <-release:
			case <-t.Context().Done():
				return
			}
		}
		response := dnsmessage.Message{
			Header:    dnsmessage.Header{ID: query.ID, Response: true},
			Questions: query.Questions,
			Answers: []dnsmessage.Resource{{
				Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: ttl},
				Body:   &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}},
			}},
		}
		data, err := response.Pack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = server.Write(data)
	}()
	return client
}

func TestDNSCacheUsesTTLAndCopiesAddresses(t *testing.T) {
	for _, ttl := range []uint32{0, 60} {
		t.Run((time.Duration(ttl) * time.Second).String(), func(t *testing.T) {
			var calls atomic.Int32
			session := newDNSSession(&dnsTestSession{dial: func(context.Context, string, string) (net.Conn, error) {
				calls.Add(1)
				return dnsTestConnection(t, ttl, nil), nil
			}})
			t.Cleanup(func() { _ = session.Close() })
			servers := []string{"192.0.2.53:53"}
			addresses, err := resolveSessionIPv4(t.Context(), session, "example.com", servers)
			if err != nil {
				t.Fatal(err)
			}
			addresses[0][0] ^= 0xff
			addresses, err = resolveSessionIPv4(t.Context(), session, "EXAMPLE.COM.", servers)
			if err != nil || len(addresses) != 1 || !addresses[0].Equal(net.IPv4(192, 0, 2, 1)) {
				t.Fatalf("addresses = %v, error = %v", addresses, err)
			}
			want := int32(1)
			if ttl == 0 {
				want = 2
			}
			if calls.Load() != want {
				t.Fatalf("DNS queries = %d, want %d", calls.Load(), want)
			}
		})
	}
}

type observedDNSContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedDNSContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestDNSCacheCoalescesLookups(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	releaseQuery := sync.OnceFunc(func() { close(release) })
	defer releaseQuery()
	var calls atomic.Int32
	session := newDNSSession(&dnsTestSession{dial: func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		started <- struct{}{}
		return dnsTestConnection(t, 60, release), nil
	}})
	t.Cleanup(func() { _ = session.Close() })
	servers := []string{"192.0.2.53:53"}
	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	defer cancelLeader()
	leaderDone := make(chan error, 1)
	go func() {
		_, err := resolveSessionIPv4(leaderCtx, session, "example.com", servers)
		leaderDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("DNS lookup did not start")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waiter := &observedDNSContext{Context: ctx, waiting: make(chan struct{})}
	waiterDone := make(chan error, 1)
	go func() {
		_, err := resolveSessionIPv4(waiter, session, "example.com", servers)
		waiterDone <- err
	}()
	select {
	case <-waiter.waiting:
	case <-time.After(time.Second):
		t.Fatal("duplicate lookup did not wait for the existing query")
	}
	cancel()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceling the waiter did not unblock it")
	}
	if calls.Load() != 1 {
		t.Fatalf("DNS queries = %d, want 1", calls.Load())
	}
	// Canceling the original caller must also leave the shared query alive.
	cancelLeader()
	select {
	case err := <-leaderDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leader error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceling the original caller did not unblock it")
	}
	releaseQuery()
	addresses, err := resolveSessionIPv4(t.Context(), session, "example.com", servers)
	if err != nil || len(addresses) != 1 || calls.Load() != 1 {
		t.Fatalf("shared query: addresses = %v, error = %v, queries = %d", addresses, err, calls.Load())
	}
}

func TestDNSCanceledQueryClosesSocket(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	written := make(chan struct{})
	go func() {
		buffer := make([]byte, 1024)
		_, _ = server.Read(buffer)
		close(written)
	}()
	session := &dnsTestSession{dial: func(context.Context, string, string) (net.Conn, error) { return client, nil }}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := querySessionA(ctx, session, "example.com", "192.0.2.53:53")
		done <- err
	}()
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("query was not sent")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled query succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("DNS read remained blocked after cancellation")
	}
}
