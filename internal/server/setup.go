package server

import (
	"runtime/debug"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/compress"
	"github.com/gofiber/fiber/v3/middleware/recover"

	webassets "github.com/ExpTechTW/proxygate/web"
)

func (s *Server) Setup() {
	app := fiber.New(fiber.Config{
		BodyLimit:    1 << 20,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
		ErrorHandler: func(c fiber.Ctx, err error) error {
			status := fiber.StatusInternalServerError
			message := err.Error()
			if fiberError, ok := err.(*fiber.Error); ok {
				status = fiberError.Code
				message = fiberError.Message
			}
			if strings.HasPrefix(c.Path(), "/api/") {
				return writeError(c, status, message)
			}
			return c.Status(status).SendString(message)
		},
	})
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(_ fiber.Ctx, recovered any) {
			s.logger.Printf("[server] panic: %v\n%s", recovered, debug.Stack())
		},
	}))
	app.Use(compress.New())
	app.Use(securityHeaders)

	app.Get("/api/version", s.version)
	app.Post("/api/login", s.login)
	api := app.Group("/api", s.auth)
	api.Post("/logout", s.logout)
	api.Get("/status", s.status)
	api.Get("/services", s.serviceStatuses)
	api.Post("/services/:name/restart", s.restartService)
	api.Get("/nodes", s.nodes)
	api.Post("/nodes/select", s.selectNode)
	api.Post("/nodes/reconnect", s.reconnectNode)
	api.Post("/nodes/:ip/speed-test", s.startSpeedTest)
	api.Get("/nodes/:ip/speed-test", s.speedTestStatus)
	api.Get("/config", s.getConfig)
	api.Put("/config", s.putConfig)
	api.Post("/filter/validate", s.validateFilter)
	api.Post("/refresh", s.refresh)
	api.Post("/restart", s.restartApplication)
	api.All("/*", func(c fiber.Ctx) error { return writeError(c, fiber.StatusNotFound, "API route not found") })
	app.Use(webassets.Handler())

	s.app = app
	s.address = s.config.Get().Web.ListenAddress
}
