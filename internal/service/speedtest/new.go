package speedtest

import (
	"log"

	"github.com/ExpTechTW/proxygate/internal/service"
)

func New(tester Tester, logger *log.Logger) *Service {
	return &Service{
		state:  service.NewState(ID),
		tester: tester,
		logger: logger,
		jobs:   make(map[string]Result),
	}
}
