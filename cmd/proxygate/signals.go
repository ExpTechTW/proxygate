//go:build !windows

package main

import (
	"os"
	"syscall"
)

func terminationSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}
}
