package speedtest

import "github.com/ExpTechTW/proxygate/internal/service"

func (s *Service) Status() service.Status { return s.state.Snapshot() }
