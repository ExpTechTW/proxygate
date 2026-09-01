package healthcheck

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
	select {
	case <-s.done:
		s.cancel, s.done = nil, nil
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop health-check service: %w", ctx.Err())
	}
}

func (s *Service) Restart(ctx context.Context) error {
	if err := s.Stop(ctx); err != nil {
		return err
	}
	s.state.Restarted()
	return s.Start()
}
