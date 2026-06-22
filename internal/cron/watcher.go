package cron

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WatchPattern 监控模式
type WatchPattern struct {
	ID          string
	Name        string
	Description string
	Pattern     string // glob 模式
	Interval    time.Duration
	LastCheck   time.Time
	LastResult  string
	Action      func(matches []string) error
}

// Watcher 文件/模式监控器
type Watcher struct {
	mu       sync.RWMutex
	patterns map[string]*WatchPattern
	engine   *Engine
	running  bool
	stopCh   chan struct{}
	onAlert  func(alert WatchAlert)
}

// WatchAlert 监控告警
type WatchAlert struct {
	PatternID   string
	PatternName string
	Time        time.Time
	Message     string
	Matches     []string
	Error       error
}

// NewWatcher 创建监控器
func NewWatcher(engine *Engine) *Watcher {
	return &Watcher{
		patterns: make(map[string]*WatchPattern),
		engine:   engine,
	}
}

// SetAlertHandler 设置告警处理器
func (w *Watcher) SetAlertHandler(handler func(alert WatchAlert)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onAlert = handler
}

// AddPattern 添加监控模式
func (w *Watcher) AddPattern(id, name, description, pattern string, interval time.Duration, action func(matches []string) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.patterns[id]; exists {
		return fmt.Errorf("pattern %s already exists", id)
	}
	wp := &WatchPattern{
		ID:          id,
		Name:        name,
		Description: description,
		Pattern:     pattern,
		Interval:    interval,
		Action:      action,
	}
	w.patterns[id] = wp
	return nil
}

// RemovePattern 移除监控模式
func (w *Watcher) RemovePattern(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.patterns[id]; !exists {
		return fmt.Errorf("pattern %s not found", id)
	}
	delete(w.patterns, id)
	return nil
}

// ListPatterns 列出所有监控模式
func (w *Watcher) ListPatterns() []*WatchPattern {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var patterns []*WatchPattern
	for _, wp := range w.patterns {
		patterns = append(patterns, wp)
	}
	return patterns
}

// Start 启动监控
func (w *Watcher) Start() {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopCh = make(chan struct{})
	w.mu.Unlock()

	go w.run()
}

// Stop 停止监控
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.running {
		return
	}

	w.running = false
	close(w.stopCh)
}

// run 监控循环
func (w *Watcher) run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.checkAll()
		}
	}
}

// checkAll 检查所有模式
func (w *Watcher) checkAll() {
	w.mu.RLock()
	patterns := make([]*WatchPattern, 0, len(w.patterns))
	for _, wp := range w.patterns {
		patterns = append(patterns, wp)
	}
	w.mu.RUnlock()

	now := time.Now()
	for _, wp := range patterns {
		// 检查间隔
		if !wp.LastCheck.IsZero() && now.Sub(wp.LastCheck) < wp.Interval {
			continue
		}

		w.checkPattern(wp, now)
	}
}

// checkPattern 检查单个模式
func (w *Watcher) checkPattern(wp *WatchPattern, now time.Time) {
	matches, err := filepath.Glob(wp.Pattern)
	if err != nil {
		w.alert(WatchAlert{
			PatternID:   wp.ID,
			PatternName: wp.Name,
			Time:        now,
			Message:     fmt.Sprintf("glob error: %v", err),
			Error:       err,
		})
		return
	}

	w.mu.Lock()
	wp.LastCheck = now
	wp.LastResult = fmt.Sprintf("%d matches", len(matches))
	w.mu.Unlock()

	if wp.Action != nil {
		if err := wp.Action(matches); err != nil {
			w.alert(WatchAlert{
				PatternID:   wp.ID,
				PatternName: wp.Name,
				Time:        now,
				Message:     fmt.Sprintf("action error: %v", err),
				Matches:     matches,
				Error:       err,
			})
			return
		}
	}

	if len(matches) > 0 {
		w.alert(WatchAlert{
			PatternID:   wp.ID,
			PatternName: wp.Name,
			Time:        now,
			Message:     fmt.Sprintf("found %d matches", len(matches)),
			Matches:     matches,
		})
	}
}

// alert 发送告警
func (w *Watcher) alert(a WatchAlert) {
	w.mu.RLock()
	handler := w.onAlert
	w.mu.RUnlock()

	if handler != nil {
		handler(a)
	}
}

// DefaultWatchActions 默认监控动作
var DefaultWatchActions = map[string]func(matches []string) error{
	"log": func(matches []string) error {
		log.Printf("[watch] found %d files: %s", len(matches), strings.Join(matches, ", "))
		return nil
	},
	"alert": func(matches []string) error {
		fmt.Fprintf(os.Stderr, "[ALERT] %d files matched\n", len(matches))
		return nil
	},
}
