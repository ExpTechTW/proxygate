package server

import (
	"context"
	"fmt"
)

func (s *Server) Stop(ctx context.Context) error {
	if err := s.app.ShutdownWithContext(ctx); err != nil {
		return fmt.Errorf("stop HTTP server: %w", err)
	}
	return nil
}
