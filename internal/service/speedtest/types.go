package speedtest

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/ExpTechTW/proxygate/internal/service"
)

const ID = "speedtest"

var ErrAlreadyRunning = errors.New("speed test is already running")
var ErrNotFound = errors.New("speed test was not found")

type Tester interface {
	TestNode(context.Context, string) (int64, error)
}

type Result struct {
	State         string `json:"state"`
	BitsPerSecond int64  `json:"bitsPerSecond,omitempty"`
	Error         string `json:"error,omitempty"`
}

type queuedJob struct {
	ip  string
	id  uint64
	ctx context.Context
}

type jobRecord struct {
	id     uint64
	result Result
	cancel context.CancelFunc
}

type Service struct {
	lifecycleMu sync.Mutex
	jobsMu      sync.RWMutex
	state       *service.State
	tester      Tester
	logger      *log.Logger
	jobs        map[string]jobRecord
	nextJobID   uint64

	ctx    context.Context
	cancel context.CancelFunc
	queue  chan queuedJob
	done   chan struct{}
}

var _ service.Service = (*Service)(nil)
