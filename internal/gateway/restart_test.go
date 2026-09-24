package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRestartable struct {
	stopErr      error
	startErr     error
	startErrOnce error
	running      atomic.Bool
	stopCalls    atomic.Int32
	startCalls   atomic.Int32
	readyAfter   time.Duration
	startedAt    time.Time
}

func (f *fakeRestartable) Stop() error {
	f.stopCalls.Add(1)
	f.running.Store(false)
	return f.stopErr
}

func (f *fakeRestartable) Start(ctx context.Context) error {
	f.startCalls.Add(1)
	if f.startErrOnce != nil && f.startCalls.Load() == 1 {
		return f.startErrOnce
	}
	if f.startErr != nil {
		return f.startErr
	}
	f.startedAt = time.Now()
	if f.readyAfter <= 0 {
		f.running.Store(true)
	}
	return nil
}

func TestRestartGatewayRecoversAfterStartError(t *testing.T) {
	gw := &fakeRestartable{startErrOnce: errors.New("transient")}
	result, err := RestartGateway(context.Background(), gw, RestartOptions{
		SettleDelay:     1 * time.Millisecond,
		ReadyTimeout:    20 * time.Millisecond,
		PollInterval:    1 * time.Millisecond,
		RecoveryTimeout: 100 * time.Millisecond,
	})
	if err == nil || !result.Recovered {
		t.Fatalf("expected recovered start error, result=%+v err=%v", result, err)
	}
	if gw.startCalls.Load() != 2 || !gw.IsRunning() {
		t.Fatalf("start calls=%d running=%v", gw.startCalls.Load(), gw.IsRunning())
	}
}

func (f *fakeRestartable) IsRunning() bool {
	if f.readyAfter > 0 && !f.running.Load() && !f.startedAt.IsZero() && time.Since(f.startedAt) >= f.readyAfter {
		f.running.Store(true)
	}
	return f.running.Load()
}

func TestRestartGatewaySuccess(t *testing.T) {
	gw := &fakeRestartable{}
	gw.running.Store(true)

	result, err := RestartGateway(context.Background(), gw, RestartOptions{
		SettleDelay:  5 * time.Millisecond,
		ReadyTimeout: time.Second,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RestartGateway: %v", err)
	}
	if gw.stopCalls.Load() != 1 || gw.startCalls.Load() != 1 {
		t.Fatalf("calls stop=%d start=%d", gw.stopCalls.Load(), gw.startCalls.Load())
	}
	if !gw.IsRunning() {
		t.Fatal("expected running")
	}
	if result.Duration <= 0 {
		t.Fatalf("expected positive duration, got %s", result.Duration)
	}
}

func TestRestartGatewayWaitsForReady(t *testing.T) {
	gw := &fakeRestartable{readyAfter: 30 * time.Millisecond}
	_, err := RestartGateway(context.Background(), gw, RestartOptions{
		SettleDelay:  5 * time.Millisecond,
		ReadyTimeout: time.Second,
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RestartGateway: %v", err)
	}
	if !gw.IsRunning() {
		t.Fatal("expected running after ready delay")
	}
}

func TestRestartGatewayStartError(t *testing.T) {
	gw := &fakeRestartable{startErr: errors.New("boom")}
	_, err := RestartGateway(context.Background(), gw, RestartOptions{
		SettleDelay: 5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected start error")
	}
}

func TestRestartGatewayReadyTimeout(t *testing.T) {
	gw := &fakeRestartable{readyAfter: time.Hour}
	_, err := RestartGateway(context.Background(), gw, RestartOptions{
		SettleDelay:  5 * time.Millisecond,
		ReadyTimeout: 40 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected ready timeout")
	}
}
