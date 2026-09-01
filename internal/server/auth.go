package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"

	"github.com/ExpTechTW/proxygate/internal/config"
)

const sessionCookieName = "proxygate_session"

func (s *Server) login(c fiber.Ctx) error {
	var input struct{ Username, Password string }
	if !decodeJSON(c, &input) {
		return nil
	}
	settings := s.config.Get()
	if input.Username != settings.Web.Username || bcrypt.CompareHashAndPassword([]byte(settings.Web.PasswordHash), []byte(input.Password)) != nil {
		time.Sleep(300 * time.Millisecond)
		return writeError(c, fiber.StatusUnauthorized, "invalid username or password")
	}
	setSessionCookie(c, settings)
	return writeJSON(c, fiber.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) logout(c fiber.Ctx) error {
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HTTPOnly: true,
		SameSite: fiber.CookieSameSiteStrictMode,
	})
	return writeJSON(c, fiber.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) auth(c fiber.Ctx) error {
	if !verifySession(s.config.Get(), c.Cookies(sessionCookieName)) {
		return writeError(c, fiber.StatusUnauthorized, "authentication required")
	}
	return c.Next()
}

func setSessionCookie(c fiber.Ctx, settings config.Config) {
	expires := time.Now().Add(12 * time.Hour)
	c.Cookie(&fiber.Cookie{
		Name:     sessionCookieName,
		Value:    signSession(settings, expires),
		Path:     "/",
		Expires:  expires,
		HTTPOnly: true,
		SameSite: fiber.CookieSameSiteStrictMode,
		Secure:   c.Secure(),
	})
}

func signSession(settings config.Config, expires time.Time) string {
	payload := settings.Web.Username + "|" + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(settings.Web.SessionSecret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifySession(settings config.Config, token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	payloadBytes, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	signature, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(settings.Web.SessionSecret))
	_, _ = mac.Write(payloadBytes)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	payload := strings.Split(string(payloadBytes), "|")
	if len(payload) != 2 || payload[0] != settings.Web.Username {
		return false
	}
	expires, err := strconv.ParseInt(payload[1], 10, 64)
	return err == nil && time.Now().Unix() < expires
}
