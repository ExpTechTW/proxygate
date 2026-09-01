package socks5

import (
	"context"
	"fmt"
)

func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.done == nil {
		return nil
	}
	s.cancel()
	_ = s.listener.Close()
	select {
	case <-s.done:
		s.cancel, s.listener, s.done = nil, nil, nil
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop SOCKS5 service: %w", ctx.Err())
	}
}

func (s *Service) Restart(ctx context.Context) error {
	if err := s.Stop(ctx); err != nil {
		return err
	}
	s.state.Restarted()
	return s.Start()
}
