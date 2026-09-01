//go:build windows

package main

import (
	"os"
	"os/exec"
)

func executeBinary(path string) error {
	command := exec.Command(path, os.Args[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Start()
}
