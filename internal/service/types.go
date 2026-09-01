package service

import (
	"context"
	"sync"
	"time"
)

// Service is a restartable background component owned by the application.
type Service interface {
	Name() string
	Start() error
	Stop(context.Context) error
	Restart(context.Context) error
	Status() Status
}

type Status struct {
	Name      string    `json:"name"`
	Running   bool      `json:"running"`
	Restarts  uint64    `json:"restarts"`
	StartedAt time.Time `json:"startedAt,omitzero"`
	StoppedAt time.Time `json:"stoppedAt,omitzero"`
	UpdatedAt time.Time `json:"updatedAt"`
	LastError string    `json:"lastError,omitempty"`
}

// State centralizes the synchronized runtime metadata shared by services.
type State struct {
	mu     sync.RWMutex
	status Status
}

func NewState(name string) *State {
	return &State{status: Status{Name: name, UpdatedAt: time.Now()}}
}

func (s *State) Started() {
	s.mu.Lock()
	s.status.Running = true
	s.status.StartedAt = time.Now()
	s.status.UpdatedAt = s.status.StartedAt
	s.status.LastError = ""
	s.mu.Unlock()
}

func (s *State) Stopped(err error) {
	s.mu.Lock()
	s.status.Running = false
	s.status.StoppedAt = time.Now()
	s.status.UpdatedAt = s.status.StoppedAt
	if err != nil {
		s.status.LastError = err.Error()
	}
	s.mu.Unlock()
}

func (s *State) Restarted() {
	s.mu.Lock()
	s.status.Restarts++
	s.status.UpdatedAt = time.Now()
	s.mu.Unlock()
}

func (s *State) Snapshot() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value := s.status
	value.UpdatedAt = time.Now()
	return value
}
