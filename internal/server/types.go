package server

import (
	"log"
	"sync/atomic"

	"github.com/gofiber/fiber/v3"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/service"
	"github.com/ExpTechTW/proxygate/internal/service/speedtest"
	"github.com/ExpTechTW/proxygate/internal/store"
	"github.com/ExpTechTW/proxygate/internal/vpn"
	"github.com/ExpTechTW/proxygate/internal/vpngate"
)

type Server struct {
	config     *config.Store
	database   *store.Store
	manager    *vpn.Manager
	refresher  *vpngate.Refresher
	speedTests *speedtest.Service
	services   []service.Service
	restart    chan<- struct{}
	restarting atomic.Bool
	logger     *log.Logger
	app        *fiber.App
	address    string
}
