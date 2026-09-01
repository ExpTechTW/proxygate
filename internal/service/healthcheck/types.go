package healthcheck

import (
	"context"
	"log"
	"sync"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/service"
	"github.com/ExpTechTW/proxygate/internal/vpn"
)

const ID = "healthcheck"

type Service struct {
	mu      sync.Mutex
	state   *service.State
	config  *config.Store
	manager *vpn.Manager
	checker *vpn.HealthChecker
	logger  *log.Logger

	cancel context.CancelFunc
	done   chan struct{}
}

var _ service.Service = (*Service)(nil)
