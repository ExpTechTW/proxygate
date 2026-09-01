package speedtest

import "errors"

// QueueManual adds one node requested by an authenticated API user. Refresh,
// health-check, and connection workflows must not enqueue speed tests.
func (s *Service) QueueManual(ip string) (Result, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.queue == nil {
		return Result{}, errors.New("speed-test service is not running")
	}
	select {
	case <-s.done:
		return Result{}, errors.New("speed-test service is not running")
	default:
	}

	s.jobsMu.Lock()
	if current := s.jobs[ip]; current.State == "running" {
		s.jobsMu.Unlock()
		return Result{}, ErrAlreadyRunning
	}
	result := Result{State: "running"}
	s.jobs[ip] = result
	select {
	case s.queue <- ip:
		s.jobsMu.Unlock()
		return result, nil
	default:
		delete(s.jobs, ip)
		s.jobsMu.Unlock()
		return Result{}, errors.New("speed-test queue is full")
	}
}

func (s *Service) Result(ip string) (Result, bool) {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	result, exists := s.jobs[ip]
	return result, exists
}
