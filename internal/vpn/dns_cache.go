package vpn

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	dnsCacheCapacity  = 256
	dnsLookupCapacity = 64
)

type dnsCacheEntry struct {
	addresses []net.IP
	expires   time.Time
}

type dnsLookup struct {
	done      chan struct{}
	addresses []net.IP
	err       error
}

// Keep cached answers and in-flight lookups scoped to one VPN session. A node
// switch must not reuse addresses resolved through the previous tunnel.
type dnsSession struct {
	Session
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	cache   map[string]dnsCacheEntry
	pending map[string]*dnsLookup
}

func newDNSSession(session Session) *dnsSession {
	ctx, cancel := context.WithCancel(context.Background())
	return &dnsSession{Session: session, ctx: ctx, cancel: cancel, cache: make(map[string]dnsCacheEntry), pending: make(map[string]*dnsLookup)}
}

func (s *dnsSession) Close() error {
	s.cancel()
	return s.Session.Close()
}

func (s *dnsSession) Diagnostics() map[string]uint64 {
	var result map[string]uint64
	if provider, ok := s.Session.(interface{ Diagnostics() map[string]uint64 }); ok {
		result = provider.Diagnostics()
	}
	if result == nil {
		result = make(map[string]uint64)
	}
	s.mu.Lock()
	result["dns_cached"] = uint64(len(s.cache))
	result["dns_pending"] = uint64(len(s.pending))
	s.mu.Unlock()
	return result
}

func (s *dnsSession) ListenPacket(network, address string) (net.PacketConn, error) {
	packets, ok := s.Session.(packetSession)
	if !ok {
		return nil, errors.New("active VPN session does not support datagrams")
	}
	return packets.ListenPacket(network, address)
}

func (s *dnsSession) resolveIPv4(ctx context.Context, host string, servers []string) ([]net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	key := host + "|" + strings.Join(servers, "|")
	now := time.Now()
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, net.ErrClosed
	}
	if entry, ok := s.cache[key]; ok {
		if now.Before(entry.expires) {
			s.mu.Unlock()
			return cloneIPs(entry.addresses), nil
		}
		delete(s.cache, key)
	}
	pending, ok := s.pending[key]
	if !ok {
		if len(s.pending) >= dnsLookupCapacity {
			s.mu.Unlock()
			return nil, errors.New("too many concurrent VPN DNS lookups")
		}
		pending = &dnsLookup{done: make(chan struct{})}
		s.pending[key] = pending
		// A single caller canceling must not abort a query shared by others.
		// Session shutdown and an overall timeout still bound its lifetime.
		go s.lookup(host, servers, key, pending)
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, net.ErrClosed
	case <-pending.done:
		return cloneIPs(pending.addresses), pending.err
	}
}

func (s *dnsSession) lookup(host string, servers []string, key string, pending *dnsLookup) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	addresses, ttl, err := lookupSessionIPv4(ctx, s.Session, host, servers)
	s.mu.Lock()
	if err == nil && ttl > 0 {
		now := time.Now()
		for key, entry := range s.cache {
			if !now.Before(entry.expires) {
				delete(s.cache, key)
			}
		}
		if len(s.cache) >= dnsCacheCapacity {
			var oldestKey string
			var oldest time.Time
			for key, entry := range s.cache {
				if oldest.IsZero() || entry.expires.Before(oldest) {
					oldestKey, oldest = key, entry.expires
				}
			}
			delete(s.cache, oldestKey)
		}
		s.cache[key] = dnsCacheEntry{addresses: cloneIPs(addresses), expires: time.Now().Add(ttl)}
	}
	pending.addresses, pending.err = addresses, err
	delete(s.pending, key)
	close(pending.done)
	s.mu.Unlock()
}

func cloneIPs(addresses []net.IP) []net.IP {
	result := make([]net.IP, len(addresses))
	for i, address := range addresses {
		result[i] = append(net.IP(nil), address...)
	}
	return result
}
