package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
)

const (
	version             = 5
	methodNone          = 0
	methodPassword      = 2
	methodRejected      = 0xff
	commandConnect      = 1
	commandUDPAssociate = 3
	replyOK             = 0
	replyGeneral        = 1
	replyCommand        = 7
	replyAddress        = 8
	addressIPv4         = 1
	addressDomain       = 3
	addressIPv6         = 4
)

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type PacketDialer interface {
	ListenPacket(string, string) (net.PacketConn, error)
}

type IPv4Resolver interface {
	ResolveIPv4(context.Context, string) ([]net.IP, error)
}

type Server struct {
	config *config.Store
	dialer Dialer
	logger *log.Logger
}

func New(configStore *config.Store, dialer Dialer, logger *log.Logger) *Server {
	return &Server{config: configStore, dialer: dialer, logger: logger}
}

func (s *Server) Run(ctx context.Context) error {
	address := s.config.Get().SOCKS5.ListenAddress
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen for SOCKS5: %w", err)
	}
	s.logger.Printf("[socks5] listening on %s", listener.Addr())
	return s.Serve(ctx, listener)
}

// Serve accepts SOCKS5 clients from an already-bound listener. Binding is kept
// outside the serving loop so a background service can report startup errors
// synchronously and restart on a new configured address.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	var connections sync.Map
	var handlers sync.WaitGroup
	serveDone := make(chan struct{})
	closeConnections := func() {
		connections.Range(func(key, _ any) bool {
			_ = key.(net.Conn).Close()
			return true
		})
	}
	defer func() {
		close(serveDone)
		_ = listener.Close()
		closeConnections()
		handlers.Wait()
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
			closeConnections()
		case <-serveDone:
		}
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept SOCKS5 connection: %w", err)
		}
		connections.Store(connection, struct{}{})
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer connections.Delete(connection)
			defer connection.Close()
			if err := s.handle(ctx, connection); err != nil {
				s.logger.Printf("[socks5] client=%s error=%v", connection.RemoteAddr(), err)
			}
		}()
	}
}

func (s *Server) handle(ctx context.Context, client net.Conn) error {
	_ = client.SetDeadline(time.Now().Add(15 * time.Second))
	settings := s.config.Get().SOCKS5
	if err := negotiate(client, settings.Username, settings.Password); err != nil {
		return err
	}
	command, network, address, err := readRequest(client)
	if err != nil {
		_ = writeReply(client, replyAddress, nil)
		return err
	}
	switch command {
	case commandConnect:
		return s.handleConnect(ctx, client, network, address)
	case commandUDPAssociate:
		return s.handleUDPAssociate(ctx, client)
	default:
		_ = writeReply(client, replyCommand, nil)
		return errors.New("only SOCKS5 CONNECT and UDP ASSOCIATE are supported")
	}
}

func (s *Server) handleConnect(ctx context.Context, client net.Conn, network, address string) error {
	_ = client.SetDeadline(time.Time{})
	dialContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	upstream, err := s.dialer.DialContext(dialContext, network, address)
	if err != nil {
		_ = writeReply(client, replyGeneral, nil)
		return fmt.Errorf("connect to %s: %w", address, err)
	}
	defer upstream.Close()
	if err := writeReply(client, replyOK, upstream.LocalAddr()); err != nil {
		return err
	}
	var wait sync.WaitGroup
	wait.Add(2)
	copyStream := func(destination, source net.Conn) {
		defer wait.Done()
		_, _ = io.Copy(destination, source)
		if closer, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
	}
	go copyStream(upstream, client)
	go copyStream(client, upstream)
	wait.Wait()
	return nil
}

func (s *Server) handleUDPAssociate(ctx context.Context, client net.Conn) error {
	packetDialer, ok := s.dialer.(PacketDialer)
	if !ok {
		_ = writeReply(client, replyGeneral, nil)
		return errors.New("active VPN session does not support UDP ASSOCIATE")
	}
	network, localAddress, err := udpRelayAddress(client)
	if err != nil {
		_ = writeReply(client, replyGeneral, nil)
		return err
	}
	relay, err := net.ListenUDP(network, localAddress)
	if err != nil {
		_ = writeReply(client, replyGeneral, nil)
		return fmt.Errorf("listen for SOCKS5 UDP relay: %w", err)
	}
	defer relay.Close()

	upstreams := make(map[string]net.PacketConn, 2)
	for _, upstreamNetwork := range []string{"udp4", "udp6"} {
		upstream, listenErr := packetDialer.ListenPacket(upstreamNetwork, ":0")
		if listenErr != nil {
			if upstreamNetwork == "udp4" {
				_ = writeReply(client, replyGeneral, nil)
				return fmt.Errorf("open VPN %s datagram socket: %w", upstreamNetwork, listenErr)
			}
			continue
		}
		upstreams[upstreamNetwork] = upstream
		defer upstream.Close()
	}
	if len(upstreams) == 0 {
		_ = writeReply(client, replyGeneral, nil)
		return errors.New("VPN session does not provide a datagram socket")
	}
	if err := writeReply(client, replyOK, relay.LocalAddr()); err != nil {
		return err
	}
	_ = client.SetDeadline(time.Time{})

	type datagram struct {
		data   []byte
		from   net.Addr
		source string
		err    error
	}
	packets := make(chan datagram, len(upstreams)+1)
	associationDone := make(chan struct{})
	defer close(associationDone)
	readPackets := func(source string, connection net.PacketConn) {
		for {
			buffer := make([]byte, 64*1024)
			n, address, readErr := connection.ReadFrom(buffer)
			packet := datagram{data: append([]byte(nil), buffer[:n]...), from: address, source: source, err: readErr}
			select {
			case packets <- packet:
			case <-ctx.Done():
				return
			case <-associationDone:
				return
			}
			if readErr != nil {
				return
			}
		}
	}
	go readPackets("local", relay)
	for upstreamNetwork, upstream := range upstreams {
		go readPackets(upstreamNetwork, upstream)
	}
	var clientUDPAddress net.Addr
	controlClosed := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, client)
		close(controlClosed)
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-controlClosed:
			return nil
		case packet := <-packets:
			if packet.err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("read SOCKS5 UDP %s datagram: %w", packet.source, packet.err)
			}
			if packet.source == "local" {
				clientUDPAddress = packet.from
				requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := s.forwardUDPRequest(requestCtx, packet.data, upstreams)
				cancel()
				if err != nil {
					s.logger.Printf("[socks5] UDP request error: %v", err)
				}
				continue
			}
			response, err := marshalUDPResponse(packet.from, packet.data)
			if err != nil {
				s.logger.Printf("[socks5] UDP response error: %v", err)
				continue
			}
			if clientUDPAddress == nil {
				continue
			}
			if _, err := relay.WriteTo(response, clientUDPAddress); err != nil {
				s.logger.Printf("[socks5] write UDP response: %v", err)
			}
		}
	}
}

func (s *Server) forwardUDPRequest(ctx context.Context, data []byte, upstreams map[string]net.PacketConn) error {
	host, port, payload, err := parseUDPRequest(data)
	if err != nil {
		return err
	}
	var destination *net.UDPAddr
	if ip := net.ParseIP(host); ip != nil {
		destination = &net.UDPAddr{IP: ip, Port: port}
	} else {
		resolver, ok := s.dialer.(IPv4Resolver)
		if !ok {
			return errors.New("VPN session does not support UDP domain resolution")
		}
		addresses, resolveErr := resolver.ResolveIPv4(ctx, host)
		if resolveErr != nil {
			return fmt.Errorf("resolve UDP destination %s: %w", host, resolveErr)
		}
		if len(addresses) == 0 || addresses[0].To4() == nil {
			return fmt.Errorf("resolve UDP destination %s: no IPv4 address", host)
		}
		destination = &net.UDPAddr{IP: addresses[0].To4(), Port: port}
	}
	network := "udp6"
	if destination.IP.To4() != nil {
		network = "udp4"
		destination.IP = destination.IP.To4()
	} else {
		destination.IP = destination.IP.To16()
	}
	upstream := upstreams[network]
	if upstream == nil {
		return fmt.Errorf("VPN session does not support %s datagrams", network)
	}
	if _, err := upstream.WriteTo(payload, destination); err != nil {
		return fmt.Errorf("write VPN UDP datagram to %s: %w", destination, err)
	}
	return nil
}

func udpRelayAddress(client net.Conn) (string, *net.UDPAddr, error) {
	local, ok := client.LocalAddr().(*net.TCPAddr)
	if !ok {
		return "", nil, errors.New("SOCKS5 client does not have a TCP local address")
	}
	ip := append(net.IP(nil), local.IP...)
	if ip == nil || ip.IsUnspecified() {
		if local.IP.To4() != nil || ip == nil {
			return "udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}, nil
		}
		return "udp6", &net.UDPAddr{IP: net.ParseIP("::1")}, nil
	}
	if ip.To4() != nil {
		return "udp4", &net.UDPAddr{IP: ip.To4()}, nil
	}
	return "udp6", &net.UDPAddr{IP: ip.To16()}, nil
}

func parseUDPRequest(data []byte) (string, int, []byte, error) {
	if len(data) < 4 || data[0] != 0 || data[1] != 0 {
		return "", 0, nil, errors.New("invalid SOCKS5 UDP request")
	}
	if data[2] != 0 {
		return "", 0, nil, errors.New("SOCKS5 UDP fragmentation is not supported")
	}
	offset := 4
	var host string
	switch data[3] {
	case addressIPv4:
		if len(data) < offset+net.IPv4len {
			return "", 0, nil, errors.New("truncated SOCKS5 UDP IPv4 address")
		}
		host = net.IP(data[offset : offset+net.IPv4len]).String()
		offset += net.IPv4len
	case addressIPv6:
		if len(data) < offset+net.IPv6len {
			return "", 0, nil, errors.New("truncated SOCKS5 UDP IPv6 address")
		}
		host = net.IP(data[offset : offset+net.IPv6len]).String()
		offset += net.IPv6len
	case addressDomain:
		if len(data) < offset+1 {
			return "", 0, nil, errors.New("truncated SOCKS5 UDP domain length")
		}
		length := int(data[offset])
		offset++
		if length == 0 || len(data) < offset+length {
			return "", 0, nil, errors.New("invalid SOCKS5 UDP domain")
		}
		host = string(data[offset : offset+length])
		offset += length
	default:
		return "", 0, nil, errors.New("unsupported SOCKS5 UDP address type")
	}
	if len(data) < offset+2 {
		return "", 0, nil, errors.New("truncated SOCKS5 UDP port")
	}
	port := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	return host, port, data[offset+2:], nil
}

func marshalUDPResponse(address net.Addr, payload []byte) ([]byte, error) {
	udpAddress, ok := address.(*net.UDPAddr)
	if !ok || udpAddress == nil || udpAddress.IP == nil {
		return nil, errors.New("VPN UDP response has no address")
	}
	ip := udpAddress.IP
	response := []byte{0, 0, 0}
	if ipv4 := ip.To4(); ipv4 != nil {
		response = append(response, addressIPv4)
		response = append(response, ipv4...)
	} else if ipv6 := ip.To16(); ipv6 != nil {
		response = append(response, addressIPv6)
		response = append(response, ipv6...)
	} else {
		return nil, errors.New("VPN UDP response has an invalid address")
	}
	response = binary.BigEndian.AppendUint16(response, uint16(udpAddress.Port))
	return append(response, payload...), nil
}

func negotiate(connection net.Conn, username, password string) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(connection, header); err != nil {
		return err
	}
	if header[0] != version || header[1] == 0 {
		return errors.New("invalid SOCKS5 greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(connection, methods); err != nil {
		return err
	}
	wanted := byte(methodNone)
	if username != "" {
		wanted = methodPassword
	}
	selected := byte(methodRejected)
	for _, method := range methods {
		if method == wanted {
			selected = wanted
			break
		}
	}
	if _, err := connection.Write([]byte{version, selected}); err != nil {
		return err
	}
	if selected == methodRejected {
		return errors.New("SOCKS5 client does not offer the required authentication method")
	}
	if selected == methodPassword {
		return authenticate(connection, username, password)
	}
	return nil
}

func authenticate(connection net.Conn, username, password string) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(connection, header); err != nil {
		return err
	}
	if header[0] != 1 || header[1] == 0 {
		return errors.New("invalid SOCKS5 username/password request")
	}
	user := make([]byte, int(header[1]))
	if _, err := io.ReadFull(connection, user); err != nil {
		return err
	}
	length := make([]byte, 1)
	if _, err := io.ReadFull(connection, length); err != nil {
		return err
	}
	secret := make([]byte, int(length[0]))
	if _, err := io.ReadFull(connection, secret); err != nil {
		return err
	}
	status := byte(1)
	if string(user) == username && string(secret) == password {
		status = 0
	}
	_, _ = connection.Write([]byte{1, status})
	if status != 0 {
		return errors.New("SOCKS5 authentication failed")
	}
	return nil
}

func readRequest(connection net.Conn) (byte, string, string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(connection, header); err != nil {
		return 0, "", "", err
	}
	if header[0] != version || header[2] != 0 {
		return 0, "", "", errors.New("invalid SOCKS5 request")
	}
	network, host := "tcp4", ""
	switch header[3] {
	case addressIPv4:
		value := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(connection, value); err != nil {
			return 0, "", "", err
		}
		host = net.IP(value).String()
	case addressIPv6:
		network = "tcp6"
		value := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(connection, value); err != nil {
			return 0, "", "", err
		}
		host = net.IP(value).String()
	case addressDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(connection, length); err != nil {
			return 0, "", "", err
		}
		if length[0] == 0 {
			return 0, "", "", errors.New("empty SOCKS5 domain")
		}
		value := make([]byte, int(length[0]))
		if _, err := io.ReadFull(connection, value); err != nil {
			return 0, "", "", err
		}
		host = string(value)
	default:
		return 0, "", "", errors.New("unsupported SOCKS5 address type")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(connection, portBytes); err != nil {
		return 0, "", "", err
	}
	return header[1], network, net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes)))), nil
}

func writeReply(connection net.Conn, status byte, address net.Addr) error {
	ip, port := net.IPv4zero, 0
	switch socketAddress := address.(type) {
	case *net.TCPAddr:
		ip, port = socketAddress.IP, socketAddress.Port
	case *net.UDPAddr:
		ip, port = socketAddress.IP, socketAddress.Port
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		reply := append([]byte{version, status, 0, addressIPv4}, ipv4...)
		reply = binary.BigEndian.AppendUint16(reply, uint16(port))
		_, err := connection.Write(reply)
		return err
	}
	reply := append([]byte{version, status, 0, addressIPv6}, ip.To16()...)
	reply = binary.BigEndian.AppendUint16(reply, uint16(port))
	_, err := connection.Write(reply)
	return err
}
