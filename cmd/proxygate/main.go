package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/ExpTechTW/proxygate/internal/buildinfo"
)

func main() {
	args := parseCommandLine()
	version := buildinfo.Current()
	if args.showVersion {
		fmt.Printf("ProxyGate %s\nRelease: %s %s %s/%s\n", version.Version, version.Release, version.GoVersion, version.OS, version.Arch)
		return
	}
	logger := log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
	logger.Printf("[app] starting ProxyGate version=%s release=%s", version.Version, version.Release)
	if err := appStart(args, logger); err != nil && !errors.Is(err, errInterrupted) {
		logger.Fatal(err)
	}
}
