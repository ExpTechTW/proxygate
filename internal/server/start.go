package server

import (
	"fmt"

	"github.com/gofiber/fiber/v3"
)

func (s *Server) Start() error {
	s.logger.Printf("[server] listening on %s", s.address)
	if err := s.app.Listen(s.address, fiber.ListenConfig{ListenerNetwork: fiber.NetworkTCP}); err != nil {
		return fmt.Errorf("start HTTP server: %w", err)
	}
	return nil
}
