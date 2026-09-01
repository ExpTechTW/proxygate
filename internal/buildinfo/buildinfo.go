package buildinfo

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	VersionMajor      = "0"
	VersionMinor      = "1"
	VersionPatch      = "0"
	VersionPreRelease = "dev"
	BuildTimestamp    string
	BuildToolchain    string
	BuildChannel      = "development"
	BuildCommit       = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Release   string `json:"release"`
	Commit    string `json:"commit"`
	Channel   string `json:"channel"`
	Toolchain string `json:"toolchain,omitempty"`
	BuildTime string `json:"buildTime,omitempty"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

func Current() Info {
	version := fmt.Sprintf("%s.%s.%s", VersionMajor, VersionMinor, VersionPatch)
	if preRelease := strings.TrimSpace(VersionPreRelease); preRelease != "" {
		version += "-" + preRelease
	}

	release := version
	commit := strings.TrimSpace(BuildCommit)
	if commit != "" && commit != "unknown" {
		release += "-" + commit
	}

	var buildTime string
	if timestamp, err := strconv.ParseInt(BuildTimestamp, 10, 64); err == nil && timestamp > 0 {
		release += fmt.Sprintf("-%d", timestamp)
		buildTime = time.Unix(timestamp, 0).UTC().Format(time.RFC3339)
	}

	return Info{
		Version: version, Release: release, Commit: commit,
		Channel: strings.TrimSpace(BuildChannel), Toolchain: strings.TrimSpace(BuildToolchain),
		BuildTime: buildTime, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
	}
}
