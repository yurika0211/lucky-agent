package lhcmd

import "github.com/yurika0211/luckyagent/internal/logger"

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

func SetBuildInfo(version, commit, date string) {
	if version != "" {
		buildVersion = version
	}
	if commit != "" {
		buildCommit = commit
	}
	if date != "" {
		buildDate = date
	}
}

func Execute() error {
	cmd := newRootCmd()
	if err := cmd.Execute(); err != nil {
		logger.Error("command failed", "error", err)
		return err
	}
	return nil
}
