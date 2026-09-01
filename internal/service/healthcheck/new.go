package healthcheck

import (
	"log"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/service"
	"github.com/ExpTechTW/proxygate/internal/vpn"
)

func New(configStore *config.Store, manager *vpn.Manager, logger *log.Logger) *Service {
	return &Service{
		state:   service.NewState(ID),
		config:  configStore,
		manager: manager,
		checker: vpn.NewHealthChecker(configStore, manager),
		logger:  logger,
	}
}
