package main

import "errors"

var errInterrupted = errors.New("application interrupted")

type exitReason int

const (
	exitInterrupt exitReason = iota
	exitRestart
	exitServerError
)
