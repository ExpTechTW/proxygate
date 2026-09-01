package server

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
)

func decodeJSON(c fiber.Ctx, target any) bool {
	decoder := json.NewDecoder(bytes.NewReader(c.Body()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		_ = writeError(c, fiber.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(c fiber.Ctx, status int, value any) error {
	return c.Status(status).JSON(value)
}

func writeError(c fiber.Ctx, status int, message string) error {
	return writeJSON(c, status, map[string]string{"error": message})
}

func boundedInteger(value string, fallback, minimum, maximum int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum {
		return fallback
	}
	if parsed > maximum {
		return maximum
	}
	return parsed
}

func securityHeaders(c fiber.Ctx) error {
	path := c.Path()
	switch {
	case strings.HasPrefix(path, "/api/"):
		c.Set(fiber.HeaderCacheControl, "no-store")
	case strings.HasPrefix(path, "/assets/"):
		c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
	default:
		c.Set(fiber.HeaderCacheControl, "no-cache")
	}
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Set(fiber.HeaderXFrameOptions, "DENY")
	c.Set(fiber.HeaderReferrerPolicy, "no-referrer")
	c.Set(fiber.HeaderContentSecurityPolicy, "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
	return c.Next()
}
