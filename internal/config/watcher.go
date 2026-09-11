package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"
)

const configReloadDebounce = 150 * time.Millisecond

type ConfigWatcher struct {
	mu                sync.RWMutex
	manager           *Manager
	cfgPath           string
	interval          time.Duration
	stopCh            chan struct{}
	onChange          func(oldCfg, newCfg *Config)
	onValidate        func(oldCfg, newCfg *Config) error
	onError           func(err error)
	running           bool
	lastDigest        [sha256.Size]byte
	lastAttemptDigest [sha256.Size]byte
	hasDigest         bool
	timer             *time.Timer
}

func NewConfigWatcher(mgr *Manager, interval time.Duration) *ConfigWatcher {
	if interval <= 0 {
		interval = time.Second
	}
	return &ConfigWatcher{
		manager:  mgr,
		cfgPath:  mgr.ConfigFile(),
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

func (w *ConfigWatcher) OnChange(fn func(oldCfg, newCfg *Config)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onChange = fn
}

func (w *ConfigWatcher) OnValidate(fn func(oldCfg, newCfg *Config) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onValidate = fn
}

func (w *ConfigWatcher) OnError(fn func(err error)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onError = fn
}

func (w *ConfigWatcher) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return fmt.Errorf("watcher already running")
	}
	if data, err := os.ReadFile(w.cfgPath); err == nil {
		w.lastDigest = sha256.Sum256(data)
		w.lastAttemptDigest = w.lastDigest
		w.hasDigest = true
	}
	w.running = true
	stopCh := w.stopCh
	go w.watchLoop(stopCh)
	return nil
}

func (w *ConfigWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.running {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	close(w.stopCh)
	w.running = false
	w.stopCh = make(chan struct{})
}

func (w *ConfigWatcher) IsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.running
}

func (w *ConfigWatcher) GetConfig() *Config {
	if w.manager == nil {
		return nil
	}
	return w.manager.Get()
}

func (w *ConfigWatcher) watchLoop(stopCh chan struct{}) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			w.checkAndReload()
		}
	}
}

func (w *ConfigWatcher) checkAndReload() {
	data, err := os.ReadFile(w.cfgPath)
	if err != nil {
		if !os.IsNotExist(err) {
			w.emitError(fmt.Errorf("read config: %w", err))
		}
		return
	}
	digest := sha256.Sum256(data)
	w.mu.RLock()
	known := w.hasDigest && (digest == w.lastDigest || digest == w.lastAttemptDigest)
	w.mu.RUnlock()
	if known {
		return
	}
	w.scheduleReload(digest)
}

func (w *ConfigWatcher) scheduleReload(digest [sha256.Size]byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.running {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.lastAttemptDigest = digest
	w.hasDigest = true
	stopCh := w.stopCh
	w.timer = time.AfterFunc(configReloadDebounce, func() {
		select {
		case <-stopCh:
			return
		default:
			w.reloadDigest(digest)
		}
	})
}

func (w *ConfigWatcher) reloadDigest(expected [sha256.Size]byte) {
	data, err := os.ReadFile(w.cfgPath)
	if err != nil {
		w.emitError(fmt.Errorf("read config: %w", err))
		return
	}
	actual := sha256.Sum256(data)
	if actual != expected {
		w.scheduleReload(actual)
		return
	}
	w.mu.RLock()
	validator := w.onValidate
	w.mu.RUnlock()
	result, err := w.manager.ReloadValidated(validator)
	if err != nil {
		w.mu.Lock()
		w.lastAttemptDigest = actual
		w.hasDigest = true
		w.timer = nil
		w.mu.Unlock()
		w.emitError(err)
		return
	}
	w.mu.Lock()
	w.lastDigest = actual
	w.lastAttemptDigest = actual
	w.hasDigest = true
	w.timer = nil
	onChange := w.onChange
	w.mu.Unlock()
	if result.Changed && onChange != nil {
		onChange(result.Old, result.New)
	}
}

func (w *ConfigWatcher) emitError(err error) {
	w.mu.RLock()
	fn := w.onError
	w.mu.RUnlock()
	if fn != nil {
		fn(err)
	}
}

func (w *ConfigWatcher) ForceReload() error {
	result, err := w.manager.ReloadValidated(func(oldCfg, newCfg *Config) error {
		w.mu.RLock()
		validator := w.onValidate
		w.mu.RUnlock()
		if validator == nil {
			return nil
		}
		return validator(oldCfg, newCfg)
	})
	if err != nil {
		return err
	}
	if data, readErr := os.ReadFile(w.cfgPath); readErr == nil {
		digest := sha256.Sum256(data)
		w.mu.Lock()
		w.lastDigest = digest
		w.lastAttemptDigest = digest
		w.hasDigest = true
		w.mu.Unlock()
	}
	w.mu.RLock()
	onChange := w.onChange
	w.mu.RUnlock()
	if result.Changed && onChange != nil {
		onChange(result.Old, result.New)
	}
	return nil
}

type ConfigDiff struct {
	ChangedFields []string
	OldValues     map[string]string
	NewValues     map[string]string
}

func DiffConfig(oldCfg, newCfg *Config) *ConfigDiff {
	diff := &ConfigDiff{
		ChangedFields: []string{},
		OldValues:     make(map[string]string),
		NewValues:     make(map[string]string),
	}
	if oldCfg.Provider != newCfg.Provider {
		diff.ChangedFields = append(diff.ChangedFields, "provider")
		diff.OldValues["provider"] = oldCfg.Provider
		diff.NewValues["provider"] = newCfg.Provider
	}
	if oldCfg.Model != newCfg.Model {
		diff.ChangedFields = append(diff.ChangedFields, "model")
		diff.OldValues["model"] = oldCfg.Model
		diff.NewValues["model"] = newCfg.Model
	}
	if oldCfg.APIBase != newCfg.APIBase {
		diff.ChangedFields = append(diff.ChangedFields, "api_base")
		diff.OldValues["api_base"] = oldCfg.APIBase
		diff.NewValues["api_base"] = newCfg.APIBase
	}
	if oldCfg.MaxTokens != newCfg.MaxTokens {
		diff.ChangedFields = append(diff.ChangedFields, "max_tokens")
		diff.OldValues["max_tokens"] = fmt.Sprintf("%d", oldCfg.MaxTokens)
		diff.NewValues["max_tokens"] = fmt.Sprintf("%d", newCfg.MaxTokens)
	}
	if oldCfg.Temperature != newCfg.Temperature {
		diff.ChangedFields = append(diff.ChangedFields, "temperature")
		diff.OldValues["temperature"] = fmt.Sprintf("%.2f", oldCfg.Temperature)
		diff.NewValues["temperature"] = fmt.Sprintf("%.2f", newCfg.Temperature)
	}
	if oldCfg.SoulPath != newCfg.SoulPath {
		diff.ChangedFields = append(diff.ChangedFields, "soul_path")
		diff.OldValues["soul_path"] = oldCfg.SoulPath
		diff.NewValues["soul_path"] = newCfg.SoulPath
	}
	return diff
}

func (d *ConfigDiff) HasChanged() bool {
	return len(d.ChangedFields) > 0
}

func (d *ConfigDiff) Format() string {
	if !d.HasChanged() {
		return "No configuration changes"
	}
	result := "Configuration changes:\n"
	for _, field := range d.ChangedFields {
		result += fmt.Sprintf("  %s: %s → %s\n", field, d.OldValues[field], d.NewValues[field])
	}
	return result
}

type ReloadResult struct {
	Old             *Config
	New             *Config
	Changed         bool
	HotReloaded     []string
	RestartRequired []string
}

func classifyReload(oldCfg, newCfg *Config) (hotReloaded, restartRequired []string) {
	if oldCfg == nil || newCfg == nil {
		return nil, nil
	}
	if !reflect.DeepEqual(oldCfg.LlmProvider, newCfg.LlmProvider) || oldCfg.APIKey != newCfg.APIKey || oldCfg.APIBase != newCfg.APIBase || !reflect.DeepEqual(oldCfg.ExtraHeaders, newCfg.ExtraHeaders) || !reflect.DeepEqual(oldCfg.Models, newCfg.Models) {
		hotReloaded = append(hotReloaded, "llm_provider")
	}
	if oldCfg.MaxTokens != newCfg.MaxTokens || oldCfg.Temperature != newCfg.Temperature || !reflect.DeepEqual(oldCfg.Limits, newCfg.Limits) || !reflect.DeepEqual(oldCfg.Retry, newCfg.Retry) || !reflect.DeepEqual(oldCfg.CircuitBreaker, newCfg.CircuitBreaker) || !reflect.DeepEqual(oldCfg.RateLimit, newCfg.RateLimit) || !reflect.DeepEqual(oldCfg.Context, newCfg.Context) || !reflect.DeepEqual(oldCfg.Agent, newCfg.Agent) || !reflect.DeepEqual(oldCfg.ModelRouter, newCfg.ModelRouter) || !reflect.DeepEqual(oldCfg.Hooks, newCfg.Hooks) || !reflect.DeepEqual(oldCfg.ToolTrace, newCfg.ToolTrace) {
		hotReloaded = append(hotReloaded, "request_runtime")
	}
	if !reflect.DeepEqual(oldCfg.Server, newCfg.Server) {
		restartRequired = append(restartRequired, "server")
	}
	if oldCfg.Dashboard != newCfg.Dashboard {
		restartRequired = append(restartRequired, "dashboard")
	}
	if !reflect.DeepEqual(oldCfg.MsgGateway, newCfg.MsgGateway) {
		restartRequired = append(restartRequired, "msg_gateway")
	}
	if !reflect.DeepEqual(oldCfg.Embedding, newCfg.Embedding) || !reflect.DeepEqual(oldCfg.RAG, newCfg.RAG) || !reflect.DeepEqual(oldCfg.Memory, newCfg.Memory) || !reflect.DeepEqual(oldCfg.Tools, newCfg.Tools) || !reflect.DeepEqual(oldCfg.Multimodal, newCfg.Multimodal) || !reflect.DeepEqual(oldCfg.ImageGeneration, newCfg.ImageGeneration) || !reflect.DeepEqual(oldCfg.TTS, newCfg.TTS) || !reflect.DeepEqual(oldCfg.Autonomy, newCfg.Autonomy) || !reflect.DeepEqual(oldCfg.Delegate, newCfg.Delegate) || !reflect.DeepEqual(oldCfg.Proactive, newCfg.Proactive) {
		restartRequired = append(restartRequired, "runtime_services")
	}
	return hotReloaded, restartRequired
}

func ReloadClassification(oldCfg, newCfg *Config) (hotReloaded, restartRequired []string) {
	return classifyReload(oldCfg, newCfg)
}

func (m *Manager) Reload() error {
	_, err := m.ReloadValidated(nil)
	return err
}

func (m *Manager) ReloadValidated(validate func(oldCfg, newCfg *Config) error) (ReloadResult, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	data, err := os.ReadFile(m.cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ReloadResult{}, nil
		}
		return ReloadResult{}, fmt.Errorf("read config: %w", err)
	}
	next, err := parseConfigData(data)
	if err != nil {
		return ReloadResult{}, fmt.Errorf("parse config: %w", err)
	}
	m.mu.RLock()
	old := cloneConfig(m.config)
	m.mu.RUnlock()
	if validate != nil {
		if err := validate(old, next); err != nil {
			return ReloadResult{}, fmt.Errorf("validate config: %w", err)
		}
	}
	hotReloaded, restartRequired := classifyReload(old, next)
	result := ReloadResult{
		Old:             old,
		New:             cloneConfig(next),
		Changed:         !reflect.DeepEqual(old, next),
		HotReloaded:     hotReloaded,
		RestartRequired: restartRequired,
	}
	m.mu.Lock()
	m.config = next
	m.mu.Unlock()
	return result, nil
}

func (m *Manager) WatchConfig(interval time.Duration) (*ConfigWatcher, error) {
	return NewConfigWatcher(m, interval), nil
}

func (m *Manager) ConfigFile() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfgPath
}

func (m *Manager) HomeDirPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.homeDir
}
