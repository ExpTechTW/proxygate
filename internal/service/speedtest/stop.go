package speedtest

import (
	"context"
	"fmt"
)

func (s *Service) Stop(ctx context.Context) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.done == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		s.cancel, s.queue, s.done = nil, nil, nil
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop speed-test service: %w", ctx.Err())
	}
}

func (s *Service) Restart(ctx context.Context) error {
	if err := s.Stop(ctx); err != nil {
		return err
	}
	s.state.Restarted()
	return s.Start()
}
