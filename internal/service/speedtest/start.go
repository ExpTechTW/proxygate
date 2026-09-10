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
			s.ctx, s.cancel, s.queue, s.done = nil, nil, nil, nil
		default:
			return errors.New("speed-test service is already running")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	queue := make(chan queuedJob, 128)
	done := make(chan struct{})
	s.ctx, s.cancel, s.queue, s.done = ctx, cancel, queue, done
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
			case job := <-queue:
				s.run(job)
			}
		}
	}()
	return nil
}

func (s *Service) run(job queuedJob) {
	s.jobsMu.RLock()
	record, exists := s.jobs[job.ip]
	shouldRun := exists && record.id == job.id && record.result.State == "running"
	s.jobsMu.RUnlock()
	if !shouldRun {
		return
	}

	speed, err := s.tester.TestNode(job.ctx, job.ip)
	result := Result{State: "complete", BitsPerSecond: speed}
	if errors.Is(job.ctx.Err(), context.Canceled) {
		result = Result{State: "canceled"}
	} else if err != nil {
		result.State = "failed"
		result.Error = err.Error()
		s.logger.Printf("[service:%s] node=%s error=%v", ID, job.ip, err)
	}
	s.jobsMu.Lock()
	record, exists = s.jobs[job.ip]
	if exists && record.id == job.id && record.result.State == "running" {
		record.result = result
		record.cancel = nil
		s.jobs[job.ip] = record
	}
	s.jobsMu.Unlock()
}

func (s *Service) failPending(queue <-chan queuedJob, cause error) {
	for {
		select {
		case job := <-queue:
			s.jobsMu.Lock()
			record, exists := s.jobs[job.ip]
			if exists && record.id == job.id && record.result.State == "running" {
				record.result = Result{State: "failed", Error: cause.Error()}
				record.cancel = nil
				s.jobs[job.ip] = record
			}
			s.jobsMu.Unlock()
		default:
			return
		}
	}
}

func (s *Service) failRunning(cause error) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	for ip, record := range s.jobs {
		if record.result.State == "running" {
			record.result = Result{State: "failed", Error: cause.Error()}
			record.cancel = nil
			s.jobs[ip] = record
		}
	}
}
