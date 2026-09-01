package speedtest

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
)

func (s *Service) Name() string { return ID }

func (s *Service) Start() error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.done != nil {
		select {
		case <-s.done:
			s.cancel, s.queue, s.done = nil, nil, nil
		default:
			return errors.New("speed-test service is already running")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	queue := make(chan string, 128)
	done := make(chan struct{})
	s.cancel, s.queue, s.done = cancel, queue, done
	s.state.Started()

	go func() {
		var runErr error
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("panic: %v", recovered)
				s.logger.Printf("[service:%s] recovered: %v\n%s", ID, recovered, debug.Stack())
				s.failRunning(runErr)
			}
			s.state.Stopped(runErr)
			close(done)
		}()
		for {
			select {
			case <-ctx.Done():
				s.failPending(queue, ctx.Err())
				return
			case ip := <-queue:
				s.run(ctx, ip)
			}
		}
	}()
	return nil
}

func (s *Service) run(ctx context.Context, ip string) {
	speed, err := s.tester.TestNode(ctx, ip)
	result := Result{State: "complete", BitsPerSecond: speed}
	if err != nil {
		result.State = "failed"
		result.Error = err.Error()
		s.logger.Printf("[service:%s] node=%s error=%v", ID, ip, err)
	}
	s.jobsMu.Lock()
	s.jobs[ip] = result
	s.jobsMu.Unlock()
}

func (s *Service) failPending(queue <-chan string, cause error) {
	for {
		select {
		case ip := <-queue:
			s.jobsMu.Lock()
			s.jobs[ip] = Result{State: "failed", Error: cause.Error()}
			s.jobsMu.Unlock()
		default:
			return
		}
	}
}

func (s *Service) failRunning(cause error) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	for ip, result := range s.jobs {
		if result.State == "running" {
			s.jobs[ip] = Result{State: "failed", Error: cause.Error()}
		}
	}
}
