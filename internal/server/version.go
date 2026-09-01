package server

import (
	"github.com/gofiber/fiber/v3"

	"github.com/ExpTechTW/proxygate/internal/buildinfo"
)

func (s *Server) version(c fiber.Ctx) error {
	return writeJSON(c, fiber.StatusOK, buildinfo.Current())
}
