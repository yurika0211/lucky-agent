package main

import (
	"os"

	"github.com/yurika0211/luckyagent/internal/cli/lhcmd"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	lhcmd.SetBuildInfo(version, commit, date)
	if err := lhcmd.Execute(); err != nil {
		os.Exit(1)
	}
}
