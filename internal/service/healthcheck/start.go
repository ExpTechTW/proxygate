package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"
)

func (s *Service) Name() string { return ID }

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.done != nil {
		select {
		case <-s.done:
			s.cancel, s.done = nil, nil
		default:
			return errors.New("health-check service is already running")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.state.Started()

	go func() {
		var runErr error
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("panic: %v", recovered)
				s.logger.Printf("[service:%s] recovered: %v\n%s", ID, recovered, debug.Stack())
			}
			s.state.Stopped(runErr)
			close(done)
		}()
		for {
			interval, _ := s.config.Get().MonitorInterval()
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			session := s.manager.Session()
			if session == nil {
				continue
			}
			if err := s.checker.CheckSession(ctx, session); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Printf("[service:%s] %v", ID, err)
				s.manager.FailoverSession(ctx, session, err)
			}
		}
	}()
	return nil
}
