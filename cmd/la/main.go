package main

import (
	"bufio"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"

	"github.com/yurika0211/luckyagent/internal/cli/lhcmd"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func loadDotEnvIfPresent() {
	file, err := os.Open(".env")
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("failed to open .env: %v", err)
		}
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := os.LookupEnv(name); exists {
			continue
		}

		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if err := os.Setenv(name, value); err != nil {
			log.Printf("failed to set .env variable %s: %v", name, err)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("failed to read .env: %v", err)
	}
}

func startPprofIfEnabled() {
	addr := os.Getenv("LA_PPROF_ADDR")
	if addr == "" {
		return
	}

	go func() {
		log.Printf("pprof listening on http://%s/debug/pprof/", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("pprof server error: %v", err)
		}
	}()
}

func main() {
	loadDotEnvIfPresent()
	startPprofIfEnabled()
	lhcmd.SetBuildInfo(version, commit, date)
	if err := lhcmd.Execute(); err != nil {
		os.Exit(1)
	}
}
