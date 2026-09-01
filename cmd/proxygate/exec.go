//go:build !windows

package main

import (
	"os"
	"syscall"
)

func executeBinary(path string) error {
	return syscall.Exec(path, os.Args, os.Environ())
}
