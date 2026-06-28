package gateway

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/yurika0211/luckyagent/internal/logger"
)

// GatewayStats tracks per-gateway statistics.
type GatewayStats struct {
	MessagesSent     int64
	MessagesReceived int64
	Errors           int64
}

// GatewayManager manages multiple platform gateway adapters.
type GatewayManager struct {
	mu       sync.RWMutex
	gateways map[string]Gateway
	stats    map[string]*GatewayStats
	handler  MessageHandler
	running  atomic.Bool
}

// NewGatewayManager creates a new GatewayManager.
func NewGatewayManager() *GatewayManager {
	return &GatewayManager{
		gateways: make(map[string]Gateway),
		stats:    make(map[string]*GatewayStats),
	}
}

// Register adds a gateway adapter to the manager.
func (gm *GatewayManager) Register(gw Gateway) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	name := gw.Name()
	if _, exists := gm.gateways[name]; exists {
		return fmt.Errorf("gateway %q already registered", name)
	}

	gm.gateways[name] = gw
	gm.stats[name] = &GatewayStats{}
	logger.Info("gateway registered", "name", name)
	return nil
}

// Unregister removes a gateway adapter from the manager.
// It stops the gateway first if it is running.
func (gm *GatewayManager) Unregister(name string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	gw, exists := gm.gateways[name]
	if !exists {
		return fmt.Errorf("gateway %q not found", name)
	}

	if gw.IsRunning() {
		if err := gw.Stop(); err != nil {
			return fmt.Errorf("stop gateway %q: %w", name, err)
		}
	}

	delete(gm.gateways, name)
	delete(gm.stats, name)
	logger.Info("gateway unregistered", "name", name)
	return nil
}

// OnMessage registers the handler that is called when any gateway receives a message.
func (gm *GatewayManager) OnMessage(handler MessageHandler) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.handler = handler
	logger.Debug("gateway message handler updated")
}

// handleMessage is called by gateways when they receive a message.
func (gm *GatewayManager) handleMessage(ctx context.Context, gwName string, msg *Message) error {
	gm.mu.RLock()
	stats, ok := gm.stats[gwName]
	gm.mu.RUnlock()

	if ok {
		atomic.AddInt64(&stats.MessagesReceived, 1)
	}

	gm.mu.RLock()
	handler := gm.handler
	gm.mu.RUnlock()

	if handler == nil {
		logger.Debug("gateway message dropped: no handler", "gateway", gwName)
		return nil
	}

	if msg != nil {
		logger.Info("gateway message received",
			"gateway", gwName,
			"chat_id", msg.Chat.ID,
			"chat_type", msg.Chat.Type.String(),
			"sender_id", msg.Sender.ID,
			"is_command", msg.IsCommand,
			"text_len", len(msg.Text),
		)
	}

	err := handler(ctx, msg)
	if err != nil {
		logger.Warn("gateway message handler failed", "gateway", gwName, "error", err)
	}
	return err
}

// RecordSent increments the sent counter for a gateway.
func (gm *GatewayManager) RecordSent(gwName string) {
	gm.mu.RLock()
	stats, ok := gm.stats[gwName]
	gm.mu.RUnlock()

	if ok {
		atomic.AddInt64(&stats.MessagesSent, 1)
	}
	logger.Debug("gateway sent counter updated", "gateway", gwName)
}

// RecordError increments the error counter for a gateway.
func (gm *GatewayManager) RecordError(gwName string) {
	gm.mu.RLock()
	stats, ok := gm.stats[gwName]
	gm.mu.RUnlock()

	if ok {
		atomic.AddInt64(&stats.Errors, 1)
	}
	logger.Warn("gateway error counter updated", "gateway", gwName)
}

// StartAll starts all registered gateways.
func (gm *GatewayManager) StartAll(ctx context.Context) error {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	for name, gw := range gm.gateways {
		logger.Info("gateway starting", "name", name)
		if err := gw.Start(ctx); err != nil {
			logger.Error("gateway start failed", "name", name, "error", err)
			return fmt.Errorf("start gateway %q: %w", name, err)
		}
		logger.Info("gateway started", "name", name)
	}

	gm.running.Store(true)
	logger.Info("all gateways started", "count", len(gm.gateways))
	return nil
}

// Start starts a specific gateway by name.
func (gm *GatewayManager) Start(ctx context.Context, name string) error {
	gm.mu.RLock()
	gw, exists := gm.gateways[name]
	gm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("gateway %q not found", name)
	}

	logger.Info("gateway starting", "name", name)
	if err := gw.Start(ctx); err != nil {
		logger.Error("gateway start failed", "name", name, "error", err)
		return fmt.Errorf("start gateway %q: %w", name, err)
	}

	logger.Info("gateway started", "name", name)
	return nil
}

// StopAll stops all running gateways.
func (gm *GatewayManager) StopAll() error {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	var firstErr error
	for name, gw := range gm.gateways {
		if gw.IsRunning() {
			logger.Info("gateway stopping", "name", name)
			if err := gw.Stop(); err != nil && firstErr == nil {
				logger.Error("gateway stop failed", "name", name, "error", err)
				firstErr = fmt.Errorf("stop gateway %q: %w", name, err)
			} else if err == nil {
				logger.Info("gateway stopped", "name", name)
			}
		}
	}

	gm.running.Store(false)
	logger.Info("all gateways stop completed")
	return firstErr
}

// Stop stops a specific gateway by name.
func (gm *GatewayManager) Stop(name string) error {
	gm.mu.RLock()
	gw, exists := gm.gateways[name]
	gm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("gateway %q not found", name)
	}

	logger.Info("gateway stopping", "name", name)
	if err := gw.Stop(); err != nil {
		logger.Error("gateway stop failed", "name", name, "error", err)
		return err
	}
	logger.Info("gateway stopped", "name", name)
	return nil
}

// Get returns a gateway by name.
func (gm *GatewayManager) Get(name string) (Gateway, bool) {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	gw, ok := gm.gateways[name]
	return gw, ok
}

// List returns the names of all registered gateways.
func (gm *GatewayManager) List() []string {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	names := make([]string, 0, len(gm.gateways))
	for name := range gm.gateways {
		names = append(names, name)
	}
	return names
}

// Stats returns the statistics for a specific gateway.
func (gm *GatewayManager) Stats(name string) (GatewayStats, bool) {
	gm.mu.RLock()
	stats, ok := gm.stats[name]
	gm.mu.RUnlock()

	if !ok {
		return GatewayStats{}, false
	}

	return GatewayStats{
		MessagesSent:     atomic.LoadInt64(&stats.MessagesSent),
		MessagesReceived: atomic.LoadInt64(&stats.MessagesReceived),
		Errors:           atomic.LoadInt64(&stats.Errors),
	}, true
}

// AllStats returns statistics for all gateways.
func (gm *GatewayManager) AllStats() map[string]GatewayStats {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	result := make(map[string]GatewayStats, len(gm.stats))
	for name, stats := range gm.stats {
		result[name] = GatewayStats{
			MessagesSent:     atomic.LoadInt64(&stats.MessagesSent),
			MessagesReceived: atomic.LoadInt64(&stats.MessagesReceived),
			Errors:           atomic.LoadInt64(&stats.Errors),
		}
	}
	return result
}

// IsRunning returns whether the manager has started gateways.
func (gm *GatewayManager) IsRunning() bool {
	return gm.running.Load()
}

// GatewayStatus represents the status of a single gateway.
type GatewayStatus struct {
	Name    string       `json:"name"`
	Running bool         `json:"running"`
	Stats   GatewayStats `json:"stats"`
}

// Status returns the status of all gateways.
func (gm *GatewayManager) Status() []GatewayStatus {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	statuses := make([]GatewayStatus, 0, len(gm.gateways))
	for name, gw := range gm.gateways {
		stats, _ := gm.Stats(name)
		statuses = append(statuses, GatewayStatus{
			Name:    name,
			Running: gw.IsRunning(),
			Stats:   stats,
		})
	}
	return statuses
}
