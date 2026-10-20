package vpn

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// resolveSessionIPv4 deliberately avoids net.Resolver. The Go resolver labels
// errors with a system nameserver (which may be IPv6) even when its Dial hook
// redirects traffic. These DNS-over-UDP A queries use only literal IPv4
// endpoints carried by the VPN session.
func resolveSessionIPv4(ctx context.Context, session Session, host string, servers []string) ([]net.IP, error) {
	if cached, ok := session.(*dnsSession); ok {
		return cached.resolveIPv4(ctx, host, servers)
	}
	addresses, _, err := lookupSessionIPv4(ctx, session, host, servers)
	return addresses, err
}

func lookupSessionIPv4(ctx context.Context, session Session, host string, servers []string) ([]net.IP, time.Duration, error) {
	var failures []error
	for _, server := range servers {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		addresses, ttl, err := querySessionA(queryCtx, session, host, server)
		cancel()
		if err == nil && len(addresses) > 0 {
			return addresses, ttl, nil
		}
		if err == nil {
			err = errors.New("response contains no A records")
		}
		failures = append(failures, fmt.Errorf("%s: %w", server, err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, 0, fmt.Errorf("IPv4 UDP DNS query for %s through VPN: %w", host, errors.Join(failures...))
}

func querySessionA(ctx context.Context, session Session, host, server string) ([]net.IP, time.Duration, error) {
	name, err := dnsmessage.NewName(strings.TrimSuffix(host, ".") + ".")
	if err != nil {
		return nil, 0, fmt.Errorf("invalid DNS name: %w", err)
	}
	var idBytes [2]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, 0, fmt.Errorf("create DNS query ID: %w", err)
	}
	id := uint16(idBytes[0])<<8 | uint16(idBytes[1])
	query := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	payload, err := query.Pack()
	if err != nil {
		return nil, 0, fmt.Errorf("encode DNS query: %w", err)
	}

	connection, err := session.DialContext(ctx, "udp4", server)
	if err != nil {
		return nil, 0, fmt.Errorf("connect UDP DNS: %w", err)
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return nil, 0, fmt.Errorf("set UDP DNS deadline: %w", err)
		}
	}
	if _, err := connection.Write(payload); err != nil {
		return nil, 0, fmt.Errorf("write UDP DNS query: %w", err)
	}
	responsePayload := make([]byte, 64*1024)
	bytesRead, err := connection.Read(responsePayload)
	if err != nil {
		return nil, 0, fmt.Errorf("read UDP DNS response: %w", err)
	}
	responsePayload = responsePayload[:bytesRead]
	if len(responsePayload) < 12 {
		return nil, 0, errors.New("UDP DNS response is too short")
	}
	var response dnsmessage.Message
	if err := response.Unpack(responsePayload); err != nil {
		return nil, 0, fmt.Errorf("decode UDP DNS response: %w", err)
	}
	if !response.Header.Response || response.Header.ID != id {
		return nil, 0, errors.New("UDP DNS response does not match query")
	}
	if len(response.Questions) != 1 || response.Questions[0].Type != dnsmessage.TypeA || !strings.EqualFold(response.Questions[0].Name.String(), name.String()) {
		return nil, 0, errors.New("UDP DNS response contains an unexpected question")
	}
	if response.Header.RCode != dnsmessage.RCodeSuccess {
		return nil, 0, fmt.Errorf("UDP DNS returned %s", response.Header.RCode)
	}
	if response.Header.Truncated {
		return nil, 0, errors.New("UDP DNS response is truncated")
	}

	addresses := make([]net.IP, 0, len(response.Answers))
	addresses = appendARecords(addresses, response.Answers)
	// CNAME records also constrain how long the resolved A records remain valid.
	ttl := ^uint32(0)
	for _, answer := range response.Answers {
		switch answer.Body.(type) {
		case *dnsmessage.AResource, *dnsmessage.CNAMEResource:
			ttl = min(ttl, answer.Header.TTL)
		}
	}
	return addresses, time.Duration(ttl) * time.Second, nil
}

func appendARecords(addresses []net.IP, resources []dnsmessage.Resource) []net.IP {
	for _, resource := range resources {
		if body, ok := resource.Body.(*dnsmessage.AResource); ok {
			addresses = append(addresses, net.IPv4(body.A[0], body.A[1], body.A[2], body.A[3]))
		}
	}
	return addresses
}
