package socks5

import (
	"log"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/service"
	server "github.com/ExpTechTW/proxygate/internal/socks5"
)

func New(configStore *config.Store, dialer server.Dialer, logger *log.Logger) *Service {
	return &Service{
		state:  service.NewState(ID),
		config: configStore,
		dialer: dialer,
		logger: logger,
	}
}
