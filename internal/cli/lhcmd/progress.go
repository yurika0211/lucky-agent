package lhcmd

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type marquee struct {
	stopOnce sync.Once
	mu       sync.RWMutex
	done     chan struct{}
	stopped  chan struct{}
	message  string
}

func startMarquee(prefix, message string) *marquee {
	m := &marquee{
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
		message: strings.TrimSpace(message),
	}

	go func() {
		defer close(m.stopped)

		frames := []string{"-", "\\", "|", "/"}
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()

		startedAt := time.Now()
		lastWidth := 0
		frameIndex := 0

		render := func() {
			line := fmt.Sprintf("%s%s %s (%ds)", prefix, frames[frameIndex%len(frames)], m.currentMessage(), int(time.Since(startedAt).Seconds()))
			frameIndex++
			if pad := lastWidth - len(line); pad > 0 {
				line += strings.Repeat(" ", pad)
			}
			lastWidth = len(line)
			fmt.Printf("\r%s", line)
		}

		render()
		for {
			select {
			case <-m.done:
				fmt.Printf("\r%s\r", strings.Repeat(" ", lastWidth))
				return
			case <-ticker.C:
				render()
			}
		}
	}()

	return m
}

func (m *marquee) Update(message string) {
	if m == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "working"
	}
	m.mu.Lock()
	m.message = message
	m.mu.Unlock()
}

func (m *marquee) currentMessage() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.message == "" {
		return "working"
	}
	return m.message
}

func (m *marquee) Stop() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() {
		close(m.done)
		<-m.stopped
	})
}
