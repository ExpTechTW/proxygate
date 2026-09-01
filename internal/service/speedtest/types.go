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

type Tester interface {
	TestNode(context.Context, string) (int64, error)
}

type Result struct {
	State         string `json:"state"`
	BitsPerSecond int64  `json:"bitsPerSecond,omitempty"`
	Error         string `json:"error,omitempty"`
}

type Service struct {
	lifecycleMu sync.Mutex
	jobsMu      sync.RWMutex
	state       *service.State
	tester      Tester
	logger      *log.Logger
	jobs        map[string]Result

	cancel context.CancelFunc
	queue  chan string
	done   chan struct{}
}

var _ service.Service = (*Service)(nil)
