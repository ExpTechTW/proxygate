package socks5

import (
	"context"
	"log"
	"net"
	"sync"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/service"
	server "github.com/ExpTechTW/proxygate/internal/socks5"
)

const ID = "socks5"

type Service struct {
	mu     sync.Mutex
	state  *service.State
	config *config.Store
	dialer server.Dialer
	logger *log.Logger

	cancel   context.CancelFunc
	listener net.Listener
	done     chan struct{}
}

var _ service.Service = (*Service)(nil)
