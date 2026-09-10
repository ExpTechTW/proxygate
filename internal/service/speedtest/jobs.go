package speedtest

import (
	"context"
	"errors"
)

// QueueManual adds one node requested by an authenticated API user. Refresh,
// health-check, and connection workflows must not enqueue speed tests.
func (s *Service) QueueManual(ip string) (Result, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.queue == nil || s.ctx == nil {
		return Result{}, errors.New("speed-test service is not running")
	}
	select {
	case <-s.done:
		return Result{}, errors.New("speed-test service is not running")
	default:
	}

	s.jobsMu.Lock()
	if current := s.jobs[ip]; current.result.State == "running" {
		s.jobsMu.Unlock()
		return Result{}, ErrAlreadyRunning
	}
	s.nextJobID++
	jobCtx, cancel := context.WithCancel(s.ctx)
	result := Result{State: "running"}
	record := jobRecord{id: s.nextJobID, result: result, cancel: cancel}
	s.jobs[ip] = record
	select {
	case s.queue <- queuedJob{ip: ip, id: record.id, ctx: jobCtx}:
		s.jobsMu.Unlock()
		return result, nil
	default:
		cancel()
		delete(s.jobs, ip)
		s.jobsMu.Unlock()
		return Result{}, errors.New("speed-test queue is full")
	}
}

func (s *Service) Result(ip string) (Result, bool) {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	record, exists := s.jobs[ip]
	return record.result, exists
}

func (s *Service) CancelManual(ip string) (Result, error) {
	s.jobsMu.Lock()
	record, exists := s.jobs[ip]
	if !exists {
		s.jobsMu.Unlock()
		return Result{}, ErrNotFound
	}
	if record.result.State != "running" {
		s.jobsMu.Unlock()
		return record.result, nil
	}
	record.result = Result{State: "canceled"}
	cancel := record.cancel
	record.cancel = nil
	s.jobs[ip] = record
	s.jobsMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return record.result, nil
}
