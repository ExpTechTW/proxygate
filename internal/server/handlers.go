package server

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/model"
	"github.com/ExpTechTW/proxygate/internal/service"
	"github.com/ExpTechTW/proxygate/internal/service/speedtest"
	"github.com/ExpTechTW/proxygate/internal/vpngate"
)

func (s *Server) status(c fiber.Ctx) error {
	ctx := c.Context()
	return writeJSON(c, fiber.StatusOK, s.manager.Status(s.refresher.Running(), s.database.LastRefresh(ctx), s.refresher.LastError()))
}

func (s *Server) serviceStatuses(c fiber.Ctx) error {
	statuses := make([]service.Status, 0, len(s.services))
	for _, backgroundService := range s.services {
		statuses = append(statuses, backgroundService.Status())
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	return writeJSON(c, fiber.StatusOK, statuses)
}

func (s *Server) restartService(c fiber.Ctx) error {
	name := c.Params("name")
	for _, backgroundService := range s.services {
		if backgroundService.Name() != name {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := backgroundService.Restart(ctx); err != nil {
			return writeError(c, fiber.StatusInternalServerError, err.Error())
		}
		return writeJSON(c, fiber.StatusOK, backgroundService.Status())
	}
	return writeError(c, fiber.StatusNotFound, "service not found")
}

func (s *Server) nodes(c fiber.Ctx) error {
	limit := boundedInteger(c.Query("limit"), 100, 1, 500)
	offset := boundedInteger(c.Query("offset"), 0, 0, 1_000_000)
	ctx := c.Context()
	nodes, err := s.database.Nodes(ctx, s.config.Get().SelectionMode, limit, offset)
	if err != nil {
		return writeError(c, fiber.StatusInternalServerError, err.Error())
	}
	if activeIP := s.manager.ActiveIP(); offset == 0 && activeIP != "" {
		activeIncluded := false
		for _, node := range nodes {
			if node.IP == activeIP {
				activeIncluded = true
				break
			}
		}
		if !activeIncluded {
			if activeNode, nodeErr := s.database.Node(ctx, activeIP); nodeErr == nil {
				nodes = append([]model.Node{activeNode}, nodes...)
				if len(nodes) > limit {
					nodes = nodes[:limit]
				}
			}
		}
	}
	return writeJSON(c, fiber.StatusOK, nodes)
}

func (s *Server) selectNode(c fiber.Ctx) error {
	var input struct {
		IP       string `json:"ip"`
		Protocol string `json:"protocol"`
	}
	if !decodeJSON(c, &input) {
		return nil
	}
	if err := s.manager.SelectManual(c.Context(), input.IP, input.Protocol); err != nil {
		return writeError(c, fiber.StatusBadGateway, err.Error())
	}
	return writeJSON(c, fiber.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) reconnectNode(c fiber.Ctx) error {
	if err := s.manager.ReconnectCurrent(c.Context()); err != nil {
		return writeError(c, fiber.StatusBadGateway, err.Error())
	}
	return writeJSON(c, fiber.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) startSpeedTest(c fiber.Ctx) error {
	result, err := s.speedTests.QueueManual(strings.Clone(c.Params("ip")))
	if err != nil {
		status := fiber.StatusServiceUnavailable
		if errors.Is(err, speedtest.ErrAlreadyRunning) {
			status = fiber.StatusConflict
		}
		return writeError(c, status, err.Error())
	}
	return writeJSON(c, fiber.StatusAccepted, result)
}

func (s *Server) speedTestStatus(c fiber.Ctx) error {
	ip := c.Params("ip")
	if result, exists := s.speedTests.Result(ip); exists {
		return writeJSON(c, fiber.StatusOK, result)
	}
	node, err := s.database.Node(c.Context(), ip)
	if err != nil || node.MeasuredAt.IsZero() {
		return writeError(c, fiber.StatusNotFound, "no speed test found for this node")
	}
	if node.SpeedTestFailed {
		return writeJSON(c, fiber.StatusOK, speedtest.Result{State: "failed"})
	}
	return writeJSON(c, fiber.StatusOK, speedtest.Result{State: "complete", BitsPerSecond: node.MeasuredBPS})
}

func (s *Server) getConfig(c fiber.Ctx) error {
	settings := s.config.Get()
	settings.Web.PasswordHash = ""
	settings.Web.SessionSecret = ""
	return writeJSON(c, fiber.StatusOK, settings)
}

func (s *Server) putConfig(c fiber.Ctx) error {
	var input struct {
		Config      config.Config `json:"config"`
		NewPassword string        `json:"newPassword"`
	}
	if !decodeJSON(c, &input) {
		return nil
	}
	current := s.config.Get()
	input.Config.Web.PasswordHash = current.Web.PasswordHash
	input.Config.Web.SessionSecret = current.Web.SessionSecret
	if input.NewPassword != "" {
		if len(input.NewPassword) < 8 {
			return writeError(c, fiber.StatusBadRequest, "new web password must contain at least 8 characters")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			return writeError(c, fiber.StatusInternalServerError, err.Error())
		}
		input.Config.Web.PasswordHash = string(hash)
	}
	if _, err := vpngate.CompileFilter(input.Config.FilterExpression); err != nil {
		return writeError(c, fiber.StatusBadRequest, err.Error())
	}
	restartRequired := current.DatabasePath != input.Config.DatabasePath || current.SOCKS5.ListenAddress != input.Config.SOCKS5.ListenAddress || current.Web.ListenAddress != input.Config.Web.ListenAddress
	if err := s.config.Save(input.Config); err != nil {
		return writeError(c, fiber.StatusBadRequest, err.Error())
	}
	setSessionCookie(c, input.Config)
	return writeJSON(c, fiber.StatusOK, map[string]any{"ok": true, "restartRequired": restartRequired})
}

func (s *Server) validateFilter(c fiber.Ctx) error {
	var input struct {
		Expression string `json:"expression"`
	}
	if !decodeJSON(c, &input) {
		return nil
	}
	if _, err := vpngate.CompileFilter(input.Expression); err != nil {
		return writeError(c, fiber.StatusBadRequest, err.Error())
	}
	return writeJSON(c, fiber.StatusOK, map[string]bool{"valid": true})
}

func (s *Server) refresh(c fiber.Ctx) error {
	if !s.refresher.Trigger() {
		return writeError(c, fiber.StatusConflict, "node refresh is already queued")
	}
	return writeJSON(c, fiber.StatusAccepted, map[string]bool{"queued": true})
}

func (s *Server) restartApplication(c fiber.Ctx) error {
	if !s.restarting.CompareAndSwap(false, true) {
		return writeError(c, fiber.StatusConflict, "application restart is already pending")
	}
	select {
	case s.restart <- struct{}{}:
		return writeJSON(c, fiber.StatusAccepted, map[string]bool{"restarting": true})
	default:
		s.restarting.Store(false)
		return writeError(c, fiber.StatusConflict, "application restart is already pending")
	}
}
