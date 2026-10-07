package lhcmd

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/yurika0211/luckyagent/internal/server"
)

func waitForServeStop(s *server.Server) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	<-sigCh
	return s.Stop()
}
