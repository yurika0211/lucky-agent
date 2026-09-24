package gateway

import (
	"context"
	"fmt"
	"time"
)

// Restartable is the minimal surface required to bounce a messaging gateway.
type Restartable interface {
	Stop() error
	Start(ctx context.Context) error
	IsRunning() bool
}

// RestartOptions controls settle delay and readiness polling after Start.
type RestartOptions struct {
	// SettleDelay waits after Stop before Start, giving the remote side time
	// to drop the previous connection. Default 300ms.
	SettleDelay time.Duration
	// ReadyTimeout bounds how long RestartGateway waits for IsRunning=true.
	// Default 15s.
	ReadyTimeout time.Duration
	// PollInterval is the readiness poll cadence. Default 100ms.
	PollInterval time.Duration
	// RecoveryTimeout bounds a best-effort second start after startup failure.
	RecoveryTimeout time.Duration
}

// RestartResult captures one gateway bounce attempt.
type RestartResult struct {
	Duration  time.Duration
	Recovered bool
}

func (o RestartOptions) withDefaults() RestartOptions {
	if o.SettleDelay <= 0 {
		o.SettleDelay = 300 * time.Millisecond
	}
	if o.ReadyTimeout <= 0 {
		o.ReadyTimeout = 15 * time.Second
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 100 * time.Millisecond
	}
	if o.RecoveryTimeout <= 0 {
		o.RecoveryTimeout = 5 * time.Second
	}
	return o
}

// RestartGateway stops a gateway, starts it again, and waits until it reports
// running (or the timeout/context expires). It does not restart the process.
func RestartGateway(ctx context.Context, gw Restartable, opts RestartOptions) (RestartResult, error) {
	started := time.Now()
	if gw == nil {
		return RestartResult{}, fmt.Errorf("gateway is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	opts = opts.withDefaults()

	if err := gw.Stop(); err != nil {
		return RestartResult{Duration: time.Since(started)}, fmt.Errorf("stop: %w", err)
	}

	timer := time.NewTimer(opts.SettleDelay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return RestartResult{Duration: time.Since(started)}, fmt.Errorf("settle: %w", ctx.Err())
	case <-timer.C:
	}

	if err := gw.Start(ctx); err != nil {
		recovered := recoverGateway(gw, opts)
		return RestartResult{Duration: time.Since(started), Recovered: recovered}, fmt.Errorf("start: %w (recovered=%t)", err, recovered)
	}

	deadline := time.Now().Add(opts.ReadyTimeout)
	for {
		if gw.IsRunning() {
			return RestartResult{Duration: time.Since(started)}, nil
		}
		if time.Now().After(deadline) {
			recovered := recoverGateway(gw, opts)
			return RestartResult{Duration: time.Since(started), Recovered: recovered}, fmt.Errorf("not ready within %s after start (recovered=%t)", opts.ReadyTimeout, recovered)
		}
		poll := time.NewTimer(opts.PollInterval)
		select {
		case <-ctx.Done():
			poll.Stop()
			return RestartResult{Duration: time.Since(started)}, fmt.Errorf("ready wait: %w", ctx.Err())
		case <-poll.C:
		}
	}
}

func recoverGateway(gw Restartable, opts RestartOptions) bool {
	ctx, cancel := context.WithTimeout(context.Background(), opts.RecoveryTimeout)
	defer cancel()
	_ = gw.Stop()
	if opts.SettleDelay > 0 {
		t := time.NewTimer(opts.SettleDelay)
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-t.C:
		}
	}
	if err := gw.Start(ctx); err != nil {
		return false
	}
	deadline := time.NewTimer(opts.RecoveryTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(opts.PollInterval)
	defer tick.Stop()
	for {
		if gw.IsRunning() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}
