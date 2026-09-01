package socks5

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"

	server "github.com/ExpTechTW/proxygate/internal/socks5"
)

func (s *Service) Name() string { return ID }

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.done != nil {
		select {
		case <-s.done:
			s.cancel, s.listener, s.done = nil, nil, nil
		default:
			return errors.New("SOCKS5 service is already running")
		}
	}

	address := s.config.Get().SOCKS5.ListenAddress
	listener, err := net.Listen("tcp", address)
	if err != nil {
		err = fmt.Errorf("start SOCKS5 service: %w", err)
		s.state.Stopped(err)
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.listener, s.done = cancel, listener, done
	s.state.Started()
	s.logger.Printf("[service:%s] listening on %s", ID, listener.Addr())

	go func() {
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("panic: %v", recovered)
				s.logger.Printf("[service:%s] recovered: %v\n%s", ID, recovered, debug.Stack())
			}
			s.state.Stopped(err)
			close(done)
		}()
		err = server.New(s.config, s.dialer, s.logger).Serve(ctx, listener)
		if err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Printf("[service:%s] stopped unexpectedly: %v", ID, err)
		} else {
			err = nil
		}
	}()
	return nil
}
