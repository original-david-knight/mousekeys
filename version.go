package main

import (
	"os"
	"runtime"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type buildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

func currentBuildInfo() buildInfo {
	return buildInfo{
		Version:   version,
		Commit:    commit,
		BuildDate: buildDate,
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}
}

func executablePath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path
}
