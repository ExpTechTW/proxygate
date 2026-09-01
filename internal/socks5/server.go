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
	version        = 5
	methodNone     = 0
	methodPassword = 2
	methodRejected = 0xff
	commandConnect = 1
	replyOK        = 0
	replyGeneral   = 1
	replyCommand   = 7
	replyAddress   = 8
	addressIPv4    = 1
	addressDomain  = 3
	addressIPv6    = 4
)

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
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
	if command != commandConnect {
		_ = writeReply(client, replyCommand, nil)
		return errors.New("only SOCKS5 CONNECT is supported")
	}
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
	if tcpAddress, ok := address.(*net.TCPAddr); ok {
		ip, port = tcpAddress.IP, tcpAddress.Port
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
