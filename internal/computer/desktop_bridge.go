package computer

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

//go:embed desktop_helper.py
var desktopHelper string

// desktopBridge owns one lazy Python/GI process. Keeping the D-Bus connection
// alive preserves portal consent and AT-SPI object references between calls.
type desktopBridge struct {
	python string
	script string
	gate   chan struct{}
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Scanner
	log    boundedBridgeLog
	closed bool
}

type boundedBridgeLog struct {
	mu   sync.Mutex
	data []byte
}

func (l *boundedBridgeLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	l.data = append(l.data, p...)
	if len(l.data) > 4096 {
		l.data = append([]byte(nil), l.data[len(l.data)-4096:]...)
	}
	return n, nil
}
func (l *boundedBridgeLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return string(l.data) }

func newDesktopBridge(python string) *desktopBridge {
	if python == "" {
		python = "python3"
	}
	return &desktopBridge{python: python, script: desktopHelper, gate: make(chan struct{}, 1)}
}

func (b *desktopBridge) start() error {
	if b.closed {
		return errors.New("computer: desktop helper is closed")
	}
	if b.cmd != nil {
		return nil
	}
	cmd := exec.Command(b.python, "-u", "-c", b.script)
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return err
	}
	cmd.Stderr = &b.log
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return fmt.Errorf("computer: start desktop helper (requires Python 3 and GI): %w", err)
	}
	b.cmd, b.in = cmd, input
	b.out = bufio.NewScanner(output)
	b.out.Buffer(make([]byte, 4096), 4<<20)
	return nil
}

func (b *desktopBridge) stop() {
	if b.cmd != nil {
		_ = b.in.Close()
		_ = b.cmd.Process.Kill()
		_ = b.cmd.Wait()
		b.cmd, b.in, b.out = nil, nil, nil
	}
}

func (b *desktopBridge) call(ctx context.Context, request any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	select {
	case b.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-b.gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.start(); err != nil {
		return err
	}
	requestData, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(requestData) > 1<<20 {
		return errors.New("computer: helper request is too large")
	}
	// Both the write and read are cancellable by terminating our owned process.
	done := make(chan error, 1)
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	input, scanner := b.in, b.out
	go func() {
		if _, err := input.Write(append(requestData, '\n')); err != nil {
			done <- err
			return
		}
		if !scanner.Scan() {
			err := scanner.Err()
			if err == nil {
				err = io.EOF
			}
			done <- err
			return
		}
		done <- json.Unmarshal(bytes.Clone(scanner.Bytes()), &response)
	}()
	select {
	case <-ctx.Done():
		b.stop()
		<-done
		return ctx.Err()
	case err := <-done:
		if err != nil {
			b.stop()
			return fmt.Errorf("computer: desktop helper: %w (%s)", err, b.log.String())
		}
	}
	if response.Error != "" {
		return fmt.Errorf("computer: %s", response.Error)
	}
	if result != nil {
		return json.Unmarshal(response.Result, result)
	}
	return nil
}

func (b *desktopBridge) Close() error {
	b.gate <- struct{}{}
	defer func() { <-b.gate }()
	b.closed = true
	b.stop()
	return nil
}
