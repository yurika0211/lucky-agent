package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Dashboard serves the embedded status and chat UI.
type Dashboard struct {
	mu        sync.RWMutex
	server    *http.Server
	addr      string
	running   bool
	providers []DataProvider
}

// DataProvider supplies data for the dashboard panels.
type DataProvider interface {
	DashboardData() map[string]interface{}
}

// Config defines the dashboard listen address.
type Config struct {
	Addr string `yaml:"addr,omitempty"`
}

// DefaultConfig returns the default dashboard address.
func DefaultConfig() Config {
	return Config{Addr: ":8765"}
}

// New creates a dashboard instance.
func New(cfg Config) *Dashboard {
	addr := cfg.Addr
	if addr == "" {
		addr = ":8765"
	}
	return &Dashboard{
		addr:      addr,
		providers: make([]DataProvider, 0),
	}
}

// AddProvider registers a dashboard data provider.
func (d *Dashboard) AddProvider(p DataProvider) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.providers = append(d.providers, p)
}

// Start launches the dashboard HTTP server.
func (d *Dashboard) Start() error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return fmt.Errorf("dashboard already running")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", d.handleStatus)
	mux.HandleFunc("/api/data", d.handleData)
	mux.HandleFunc("/api/health", d.handleHealth)
	mux.HandleFunc("/", d.handleSPA)

	d.server = &http.Server{
		Addr:         d.addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	d.running = true
	d.mu.Unlock()

	go func() {
		if err := d.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Dashboard server error: %v\n", err)
		}
	}()

	fmt.Printf("Dashboard running at http://localhost%s\n", d.addr)
	return nil
}

// Stop shuts the dashboard down.
func (d *Dashboard) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.running || d.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := d.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown dashboard: %w", err)
	}

	d.running = false
	return nil
}

// IsRunning reports whether the dashboard is running.
func (d *Dashboard) IsRunning() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.running
}

// Addr returns the dashboard listen address.
func (d *Dashboard) Addr() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.addr
}

func (d *Dashboard) handleStatus(w http.ResponseWriter, _ *http.Request) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	status := map[string]interface{}{
		"running":   d.running,
		"addr":      d.addr,
		"timestamp": time.Now().Format(time.RFC3339),
		"version":   "v0.9.0",
	}
	for _, p := range d.providers {
		for k, v := range p.DashboardData() {
			status[k] = v
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (d *Dashboard) handleData(w http.ResponseWriter, _ *http.Request) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	data := make(map[string]interface{})
	for _, p := range d.providers {
		for k, v := range p.DashboardData() {
			data[k] = v
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

func (d *Dashboard) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (d *Dashboard) handleSPA(w http.ResponseWriter, r *http.Request) {
	staticDir := dashboardStaticDir()

	path := r.URL.Path
	if path == "/" || path == "" {
		path = "/index.html"
	}

	filePath := filepath.Join(staticDir, path)
	data, err := os.ReadFile(filePath)
	if err != nil {
		d.serveEmbeddedSPA(w, r)
		return
	}

	contentType := "text/plain"
	switch {
	case strings.HasSuffix(path, ".html"):
		contentType = "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		contentType = "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		contentType = "application/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".json"):
		contentType = "application/json; charset=utf-8"
	}

	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func dashboardStaticDir() string {
	if staticDir := os.Getenv("LH_DASHBOARD_STATIC"); staticDir != "" {
		return staticDir
	}

	if cwd, err := os.Getwd(); err == nil {
		candidates := []string{
			filepath.Join(cwd, "UI", "GUI", "dist"),
			filepath.Join(cwd, "..", "UI", "GUI", "dist"),
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}

	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		candidates := []string{
			filepath.Join(base, "UI", "GUI", "dist"),
			filepath.Join(base, "..", "UI", "GUI", "dist"),
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}

	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".luckyharness", "dashboard")
}

func (d *Dashboard) serveEmbeddedSPA(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "" {
		http.NotFound(w, r)
		return
	}

	html := `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>LuckyHarness Dashboard</title>
  <style>
    :root {
      --bg: #eef4ea;
      --panel: rgba(255,255,250,.88);
      --panel-strong: rgba(255,255,255,.96);
      --line: rgba(35,72,47,.12);
      --text: #16311d;
      --muted: #63746b;
      --accent: #2f8f4e;
      --accent-strong: #236b3b;
      --danger: #b54b37;
      --warn: #c79b31;
      --shadow: 0 28px 70px rgba(27,53,34,.12);
      --shadow-soft: 0 18px 36px rgba(27,53,34,.08);
      --radius: 24px;
      --font-sans: "Segoe UI","PingFang SC","Microsoft YaHei",sans-serif;
      --font-display: Georgia,"Times New Roman",serif;
      --font-mono: "Cascadia Code","JetBrains Mono",monospace;
    }
    * { box-sizing: border-box; }
    html, body { min-height: 100%; }
    body {
      margin: 0;
      color: var(--text);
      font-family: var(--font-sans);
      background:
        radial-gradient(circle at 14% 18%, rgba(47,143,78,.12), transparent 24%),
        radial-gradient(circle at 88% 10%, rgba(201,231,205,.85), transparent 20%),
        linear-gradient(135deg, #f7faf4 0%, #edf5ee 48%, #f5f5ef 100%);
    }
    .shell { min-height: 100vh; padding: 24px; }
    .layout {
      min-height: calc(100vh - 48px);
      display: grid;
      grid-template-columns: 332px minmax(0, 1fr);
      gap: 20px;
    }
    .sidebar, .workspace {
      border: 1px solid rgba(35,72,47,.08);
      box-shadow: var(--shadow);
      backdrop-filter: blur(16px);
    }
    .sidebar {
      border-radius: 30px;
      padding: 28px;
      background: linear-gradient(165deg, rgba(237,248,240,.98), rgba(224,241,228,.9));
      display: grid;
      gap: 18px;
      align-content: start;
    }
    .brand-row, .top-row, .row {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: center;
    }
    .brand {
      display: inline-flex;
      align-items: center;
      gap: 12px;
      font-size: 13px;
      letter-spacing: .14em;
      text-transform: uppercase;
      font-weight: 800;
      color: var(--accent-strong);
    }
    .clover {
      width: 42px;
      height: 42px;
      position: relative;
      transform: rotate(45deg);
      flex: 0 0 auto;
    }
    .clover span, .clover::after {
      content: "";
      position: absolute;
      width: 21px;
      height: 21px;
      border-radius: 50% 50% 42% 50%;
      background: linear-gradient(135deg, #5cbc7b, #2f8f4e);
      box-shadow: 0 8px 16px rgba(35,107,59,.18);
    }
    .clover span:nth-child(1) { left: 0; top: 10px; }
    .clover span:nth-child(2) { right: 0; top: 10px; transform: rotate(90deg); }
    .clover span:nth-child(3) { left: 10px; top: 0; transform: rotate(-90deg); }
    .clover::after { left: 10px; bottom: 0; transform: rotate(180deg); }
    .chip, .pill {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      border-radius: 999px;
      font-size: 12px;
    }
    .chip {
      padding: 10px 14px;
      background: rgba(255,255,255,.78);
      border: 1px solid rgba(35,72,47,.08);
      color: var(--muted);
    }
    .pill {
      padding: 8px 12px;
      background: rgba(255,255,255,.9);
      border: 1px solid rgba(35,72,47,.08);
      color: var(--muted);
    }
    .dot {
      width: 10px;
      height: 10px;
      border-radius: 999px;
      background: var(--warn);
      box-shadow: 0 0 0 4px rgba(199,155,49,.12);
    }
    .dot.ok { background: var(--accent); box-shadow: 0 0 0 4px rgba(47,143,78,.14); }
    .dot.err { background: var(--danger); box-shadow: 0 0 0 4px rgba(181,75,55,.12); }
    .hero h1 {
      margin: 0;
      font-family: var(--font-display);
      font-size: clamp(44px, 5vw, 68px);
      line-height: .95;
      letter-spacing: -.04em;
    }
    .hero p, .hint, .subtle, .meta-line { color: var(--muted); line-height: 1.7; }
    .eyebrow, .label {
      font-size: 12px;
      font-weight: 800;
      letter-spacing: .12em;
      text-transform: uppercase;
      color: var(--accent-strong);
    }
    .card {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: var(--radius);
      box-shadow: var(--shadow-soft);
    }
    .card-head {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 12px;
      padding: 18px 18px 0;
    }
    .card-title { margin: 0; font-size: 16px; font-weight: 800; }
    .card-body { padding: 18px; }
    .stack { display: grid; gap: 14px; }
    .metrics-grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 12px;
    }
    .metric {
      background: rgba(255,255,255,.85);
      border: 1px solid rgba(35,72,47,.08);
      border-radius: 18px;
      padding: 14px 16px;
    }
    .metric-value { margin-top: 8px; font-size: 24px; line-height: 1; font-weight: 800; }
    .kv { display: grid; gap: 8px; }
    .kv-row {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      padding-bottom: 8px;
      border-bottom: 1px dashed rgba(35,72,47,.1);
      font-size: 13px;
    }
    .kv-row:last-child { border-bottom: none; padding-bottom: 0; }
    .kv-key { color: var(--muted); }
    .kv-value { text-align: right; word-break: break-word; }
    label {
      display: grid;
      gap: 7px;
      font-size: 13px;
      color: var(--muted);
      font-weight: 600;
    }
    input, textarea, button { font: inherit; }
    input, textarea {
      width: 100%;
      border: 1px solid rgba(35,72,47,.1);
      background: rgba(255,255,255,.95);
      border-radius: 16px;
      color: var(--text);
      outline: none;
      padding: 13px 14px;
    }
    textarea {
      resize: vertical;
      min-height: 120px;
      font-family: var(--font-sans);
    }
    button {
      border: none;
      border-radius: 999px;
      padding: 11px 18px;
      cursor: pointer;
    }
    button:disabled { cursor: not-allowed; opacity: .55; }
    .btn-primary {
      background: linear-gradient(135deg, var(--accent), var(--accent-strong));
      color: #f7fff8;
      font-weight: 800;
    }
    .btn-secondary {
      background: rgba(255,255,255,.9);
      color: var(--text);
      border: 1px solid rgba(35,72,47,.1);
    }
    .workspace {
      border-radius: 34px;
      background: rgba(255,255,255,.55);
      padding: 22px;
      display: grid;
      grid-template-rows: auto auto minmax(0, 1fr);
      gap: 16px;
    }
    .top-row {
      align-items: flex-start;
    }
    .top-copy h2 {
      margin: 0;
      font-size: 28px;
      line-height: 1.08;
      letter-spacing: -.03em;
    }
    .top-copy p { margin: 6px 0 0; }
    .status-row {
      display: flex;
      flex-wrap: wrap;
      justify-content: flex-end;
      gap: 10px;
    }
    .top-bar {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 12px;
    }
    .bar {
      padding: 14px 16px;
      border-radius: 18px;
      background: rgba(255,255,255,.8);
      border: 1px solid rgba(35,72,47,.08);
    }
    .bar .label { display: block; margin-bottom: 8px; }
    .bar .value { font-size: 16px; font-weight: 800; }
    .main-grid {
      min-height: 0;
      display: grid;
      grid-template-columns: minmax(0, 1fr) 320px;
      gap: 16px;
    }
    .chat-card {
      min-height: 0;
      display: grid;
      grid-template-rows: auto minmax(0, 1fr) auto;
      overflow: hidden;
      background: var(--panel-strong);
    }
    .chat-log, .tool-feed {
      display: grid;
      gap: 16px;
      min-height: 0;
      overflow-y: auto;
      padding-right: 6px;
    }
    .empty {
      padding: 28px 14px 12px;
      text-align: center;
    }
    .empty h4 {
      margin: 0;
      font-family: var(--font-display);
      font-size: 40px;
      line-height: 1.02;
      letter-spacing: -.04em;
    }
    .empty p {
      margin: 12px auto 0;
      max-width: 620px;
      color: var(--muted);
      line-height: 1.7;
    }
    .chip-row {
      display: flex;
      flex-wrap: wrap;
      justify-content: center;
      gap: 10px;
      margin-top: 16px;
    }
    .msg-row {
      display: flex;
      gap: 12px;
      align-items: flex-start;
      width: min(100%, 780px);
      margin: 0 auto;
    }
    .msg-row.user { justify-content: flex-end; }
    .msg-avatar {
      width: 34px;
      height: 34px;
      border-radius: 12px;
      flex: 0 0 auto;
      display: flex;
      align-items: center;
      justify-content: center;
      font-size: 12px;
      font-weight: 800;
      color: #f7fff8;
      background: linear-gradient(135deg, #5cbc7b, #2f8f4e);
    }
    .msg-row.user .msg-avatar {
      order: 2;
      background: linear-gradient(135deg, #7f8d85, #66726b);
    }
    .msg-card { min-width: 0; width: 100%; }
    .msg-row.user .msg-card {
      width: auto;
      max-width: min(78%, 640px);
      padding: 16px 18px;
      border-radius: 24px;
      background: linear-gradient(135deg, rgba(47,143,78,.14), rgba(47,143,78,.05));
      border: 1px solid rgba(47,143,78,.12);
    }
    .msg-meta {
      display: flex;
      justify-content: space-between;
      gap: 10px;
      margin-bottom: 8px;
      font-size: 12px;
      color: var(--muted);
    }
    .msg-name { font-weight: 800; color: var(--text); }
    .msg-body {
      white-space: pre-wrap;
      word-break: break-word;
      line-height: 1.7;
      font-size: 15px;
    }
    .mono, .tool-item .name, .msg-body code { font-family: var(--font-mono); }
    .tool-item {
      background: rgba(255,255,255,.85);
      border: 1px solid rgba(35,72,47,.08);
      border-radius: 16px;
      padding: 12px 14px;
    }
    .tool-item .name {
      margin-bottom: 6px;
      font-size: 12px;
      font-weight: 800;
      letter-spacing: .08em;
      text-transform: uppercase;
      color: var(--accent-strong);
    }
    .tool-item .desc {
      white-space: pre-wrap;
      word-break: break-word;
      color: var(--text);
      font-size: 13px;
      line-height: 1.6;
    }
    .composer {
      margin: 0 18px 18px;
      padding: 14px;
      border-radius: 24px;
      background: rgba(255,255,255,.94);
      border: 1px solid rgba(35,72,47,.08);
      box-shadow: 0 18px 34px rgba(27,53,34,.1);
      display: grid;
      gap: 12px;
    }
    .composer-top {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: center;
      flex-wrap: wrap;
    }
    .composer-hint { font-size: 12px; color: var(--muted); }
    .composer-actions {
      display: flex;
      flex-wrap: wrap;
      justify-content: space-between;
      gap: 12px;
      align-items: center;
    }
    .composer-send {
      display: flex;
      gap: 10px;
      align-items: center;
    }
    .raw-log {
      white-space: pre-wrap;
      word-break: break-word;
      max-height: 260px;
      overflow: auto;
      font-family: var(--font-mono);
      font-size: 12px;
      line-height: 1.6;
      color: #1f3526;
      background: rgba(247,250,247,.9);
      border: 1px solid rgba(35,72,47,.08);
      border-radius: 16px;
      padding: 14px;
    }
    @media (max-width: 1180px) {
      .layout, .main-grid, .top-bar { grid-template-columns: 1fr; }
      .sidebar { order: 2; }
    }
    @media (max-width: 720px) {
      .shell { padding: 0; }
      .layout { min-height: 100vh; gap: 0; }
      .sidebar, .workspace { border-radius: 0; padding: 18px 16px; }
      .metrics-grid { grid-template-columns: 1fr; }
      .composer-send { width: 100%; justify-content: space-between; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <div class="layout">
      <aside class="sidebar">
        <div class="brand-row">
          <div class="brand">
            <div class="clover"><span></span><span></span><span></span></div>
            LuckyHarness
          </div>
          <div class="chip"><span class="dot" id="runtimeDot"></span><span id="runtimeSummary">Loading</span></div>
        </div>
        <div class="hero">
          <div class="eyebrow">Clover Dashboard</div>
          <h1>Chat-first control center</h1>
          <p>把聊天放在中心，状态和配置收进侧栏。四叶草负责品牌，交互保持像 ChatGPT 一样直接。</p>
        </div>
        <section class="card">
          <div class="card-head">
            <h3 class="card-title">Connection</h3>
            <div class="chip"><span class="dot" id="socketDot"></span><span id="socketState">Disconnected</span></div>
          </div>
          <div class="card-body stack">
            <label>
              API Base
              <input id="apiBaseInput" type="text" value="http://127.0.0.1:9090" spellcheck="false">
            </label>
            <label>
              Session ID
              <input id="sessionInput" type="text" value="dashboard-main" spellcheck="false">
            </label>
            <div class="row">
              <button class="btn-primary" id="connectBtn" type="button">Connect</button>
              <button class="btn-secondary" id="disconnectBtn" type="button" disabled>Disconnect</button>
            </div>
            <div class="hint">连接成功后再发送消息。WebSocket 地址格式：<span class="mono">ws://.../api/v1/ws?session=...</span>。</div>
          </div>
        </section>
        <section class="card">
          <div class="card-head">
            <h3 class="card-title">Gateway</h3>
            <div class="chip">Addr <span class="mono" id="dashboardAddr">localhost:8765</span></div>
          </div>
          <div class="card-body"><div class="kv" id="telegramPanel"></div></div>
        </section>
        <section class="card">
          <div class="card-head"><h3 class="card-title">Sessions / Cron</h3></div>
          <div class="card-body">
            <div class="kv" id="sessionPanel"></div>
            <div style="height: 16px"></div>
            <div class="kv" id="cronPanel"></div>
          </div>
        </section>
      </aside>
      <main class="workspace">
        <div class="top-row">
          <div class="top-copy">
            <div class="eyebrow">LuckyHarness Dashboard</div>
            <h2>Chat UI</h2>
            <p>页面重点就是对话区，其他信息降噪处理。</p>
          </div>
          <div class="status-row">
            <div class="pill"><span class="dot" id="streamStateDot"></span><span id="streamState">Idle</span></div>
            <div class="pill">API <span class="mono" id="apiBaseBadge">http://127.0.0.1:9090</span></div>
            <div class="pill">Session <span class="mono" id="sessionBadge">dashboard-main</span></div>
            <button class="btn-secondary" id="refreshBtn" type="button">Refresh</button>
          </div>
        </div>
        <div class="top-bar">
          <div class="bar"><span class="label">Status</span><div class="value" id="heroStatus">Loading</div></div>
          <div class="bar"><span class="label">Provider</span><div class="value" id="providerValue">N/A</div></div>
          <div class="bar"><span class="label">Model</span><div class="value" id="modelValue">N/A</div></div>
          <div class="bar"><span class="label">Requests</span><div class="value" id="requestValue">0</div></div>
        </div>
        <section class="main-grid">
          <section class="card chat-card">
            <div class="card-head">
              <h3 class="card-title">Conversation</h3>
              <div class="chip"><span class="dot" id="composerDot"></span><span id="composerState">Ready when connected</span></div>
            </div>
            <div class="card-body" style="min-height:0;">
              <div class="empty" id="heroEmpty">
                <div class="eyebrow">Chat Workspace</div>
                <h4>Talk to LuckyHarness</h4>
                <p>像 ChatGPT 一样开始对话。四叶草只负责风格，消息流、状态和工具输出都在同一个工作区里。</p>
                <div class="chip-row">
                  <div class="chip">Chat-first</div>
                  <div class="chip">Streaming replies</div>
                  <div class="chip">Clover runtime</div>
                </div>
              </div>
              <div class="chat-log" id="chatLog"></div>
            </div>
            <div class="composer">
              <div class="composer-top">
                <div class="composer-hint">按 <span class="mono">Ctrl+Enter</span> 发送当前消息。</div>
                <div class="chip">Session <span class="mono" id="sessionComposerBadge">dashboard-main</span></div>
              </div>
              <label>
                Message
                <textarea id="messageInput" placeholder="输入消息，开始新的对话。"></textarea>
              </label>
              <div class="composer-actions">
                <div class="pill"><span class="dot" id="composerStateDot"></span><span id="composerStateText">Ready when connected</span></div>
                <div class="composer-send">
                  <button class="btn-secondary" id="clearBtn" type="button">Clear</button>
                  <button class="btn-primary" id="sendBtn" type="button" disabled>Send</button>
                </div>
              </div>
            </div>
          </section>
          <section class="stack">
            <section class="card">
              <div class="card-head"><h3 class="card-title">Reasoning / Tool Feed</h3></div>
              <div class="card-body"><div class="tool-feed" id="toolFeed"></div></div>
            </section>
            <section class="card">
              <div class="card-head"><h3 class="card-title">Raw Data</h3></div>
              <div class="card-body"><div class="raw-log" id="rawLog">Waiting for data...</div></div>
            </section>
          </section>
        </section>
      </main>
    </div>
  </div>
  <script>
    const state = { ws: null, connected: false, currentAssistantId: null, currentAssistantBody: '', rawData: null };
    const els = {
      heroStatus: document.getElementById('heroStatus'),
      dashboardAddr: document.getElementById('dashboardAddr'),
      apiBaseBadge: document.getElementById('apiBaseBadge'),
      sessionBadge: document.getElementById('sessionBadge'),
      runtimeDot: document.getElementById('runtimeDot'),
      runtimeSummary: document.getElementById('runtimeSummary'),
      providerValue: document.getElementById('providerValue'),
      modelValue: document.getElementById('modelValue'),
      requestValue: document.getElementById('requestValue'),
      telegramPanel: document.getElementById('telegramPanel'),
      sessionPanel: document.getElementById('sessionPanel'),
      cronPanel: document.getElementById('cronPanel'),
      rawLog: document.getElementById('rawLog'),
      apiBaseInput: document.getElementById('apiBaseInput'),
      sessionInput: document.getElementById('sessionInput'),
      messageInput: document.getElementById('messageInput'),
      chatLog: document.getElementById('chatLog'),
      heroEmpty: document.getElementById('heroEmpty'),
      sessionComposerBadge: document.getElementById('sessionComposerBadge'),
      toolFeed: document.getElementById('toolFeed'),
      socketDot: document.getElementById('socketDot'),
      socketState: document.getElementById('socketState'),
      composerDot: document.getElementById('composerDot'),
      composerState: document.getElementById('composerState'),
      composerStateDot: document.getElementById('composerStateDot'),
      composerStateText: document.getElementById('composerStateText'),
      streamStateDot: document.getElementById('streamStateDot'),
      streamState: document.getElementById('streamState'),
      refreshBtn: document.getElementById('refreshBtn'),
      connectBtn: document.getElementById('connectBtn'),
      sendBtn: document.getElementById('sendBtn'),
      disconnectBtn: document.getElementById('disconnectBtn'),
      clearBtn: document.getElementById('clearBtn'),
    };
    function normalizeAPIBase(value) {
      const raw = String(value || '').trim();
      if (!raw) return '';
      const defaultHost = window.location.hostname || '127.0.0.1';
      const defaultScheme = window.location.protocol === 'https:' ? 'https://' : 'http://';
      let target = raw;
      if (/^\d+$/.test(target)) target = defaultHost + ':' + target;
      else if (/^:\d+$/.test(target)) target = defaultHost + target;
      else if (/^\/\//.test(target)) target = window.location.protocol + target;
      else if (/^wss?:\/\//i.test(target)) target = target.replace(/^ws/i, 'http');
      else if (!/^https?:\/\//i.test(target)) target = defaultScheme + target;
      try {
        const url = new URL(target);
        if (!url.hostname || url.hostname === '0.0.0.0' || url.hostname === '::') {
          url.hostname = defaultHost;
        }
        return (url.protocol + '//' + url.host).replace(/\/+$/, '');
      } catch (error) {
        return target.replace(/\/+$/, '');
      }
    }
    function apiBase() {
      return els.apiBaseInput.value.trim().replace(/\/+$/, '') || window.location.origin;
    }
    function sessionID() {
      return els.sessionInput.value.trim() || 'dashboard-main';
    }
    function wsURL() {
      const url = new URL(apiBase());
      url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
      url.pathname = '/api/v1/ws';
      url.search = new URLSearchParams({ session: sessionID() }).toString();
      return url.toString();
    }
    function escapeHtml(value) {
      return String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
    }
    function pretty(value) {
      if (value === null || value === undefined || value === '') return 'N/A';
      if (Array.isArray(value)) return value.length ? value.join(', ') : 'N/A';
      return String(value);
    }
    function syncBadges() {
      els.apiBaseBadge.textContent = apiBase();
      els.sessionBadge.textContent = sessionID();
      els.sessionComposerBadge.textContent = sessionID();
    }
    function setState(el, text, kind) {
      el.textContent = text;
      const dot = el.previousElementSibling;
      if (!dot || !dot.classList) return;
      dot.className = 'dot';
      if (kind === 'ok') dot.classList.add('ok');
      if (kind === 'err') dot.classList.add('err');
    }
    function setSocketState(text, kind) {
      setState(els.socketState, text, kind);
      setState(els.composerState, text, kind);
      setState(els.composerStateText, text, kind);
    }
    function setStreamState(text, kind) {
      setState(els.streamState, text, kind);
    }
    function setRuntimeState(ok, summary) {
      els.runtimeDot.className = 'dot';
      if (ok) els.runtimeDot.classList.add('ok');
      else els.runtimeDot.classList.add('err');
      els.runtimeSummary.textContent = summary;
      els.heroStatus.textContent = summary;
    }
    function renderKV(target, rows) {
      target.innerHTML = rows.map(([k, v]) => '<div class="kv-row"><span class="kv-key">' + escapeHtml(k) + '</span><span class="kv-value">' + escapeHtml(pretty(v)) + '</span></div>').join('');
    }
    function renderData(data) {
      state.rawData = data;
      els.providerValue.textContent = data.provider || 'N/A';
      els.modelValue.textContent = data.model || 'N/A';
      els.requestValue.textContent = pretty(data.total_requests ?? 0);
      renderKV(els.telegramPanel, [
        ['Platform', data.telegram_platform || 'telegram'],
        ['Registered', data.telegram_registered ? 'yes' : 'no'],
        ['Connected', data.telegram_connected ? 'yes' : 'no'],
        ['Proxy', data.telegram_proxy || 'none'],
        ['Timeout', data.telegram_timeout_seconds ?? 'N/A'],
        ['Received', data.telegram_messages_received ?? 0],
        ['Sent', data.telegram_messages_sent ?? 0],
        ['Errors', data.telegram_errors ?? 0],
      ]);
      const sessions = (data.sessions_recent || []).map((item, index) => ['#' + (index + 1) + ' ' + (item.title || item.id || 'untitled'), (item.message_count ?? 0) + ' msg']);
      renderKV(els.sessionPanel, sessions.length ? sessions : [['Recent Sessions', 'none']]);
      const cronRows = [['Running', data.cron_running ? 'yes' : 'no'], ['Jobs', data.cron_jobs_total ?? 0]];
      for (const job of (data.cron_jobs || []).slice(0, 3)) cronRows.push([job.id || 'job', job.status || 'unknown']);
      renderKV(els.cronPanel, cronRows);
      els.rawLog.textContent = JSON.stringify(data, null, 2);
      setRuntimeState(!!data.running, data.running ? 'Runtime online' : 'Runtime unavailable');
    }
    async function refreshData() {
      syncBadges();
      try {
        const [statusRes, dataRes] = await Promise.all([fetch('/api/status'), fetch('/api/data')]);
        const status = await statusRes.json();
        const data = await dataRes.json();
        const desiredAPIBase = normalizeAPIBase(data.api_addr || status.api_addr || '');
        if (desiredAPIBase && (!state.connected || !els.apiBaseInput.value.trim())) els.apiBaseInput.value = desiredAPIBase;
        els.dashboardAddr.textContent = status.addr || location.host;
        syncBadges();
        renderData({ ...status, ...data });
      } catch (error) {
        setRuntimeState(false, 'Dashboard data fetch failed');
        els.rawLog.textContent = String(error);
      }
    }
    function appendBubble(kind, title, body, meta) {
      if (els.heroEmpty) els.heroEmpty.style.display = 'none';
      const id = 'msg-' + Date.now() + '-' + Math.random().toString(16).slice(2);
      const node = document.createElement('article');
      const avatar = kind === 'user' ? 'You' : kind === 'assistant' ? 'LH' : kind === 'error' ? '!' : 'i';
      node.className = 'msg-row ' + kind;
      node.dataset.msgId = id;
      node.innerHTML = '<div class="msg-avatar">' + escapeHtml(avatar) + '</div><div class="msg-card"><div class="msg-meta"><span class="msg-name">' + escapeHtml(title) + '</span><span>' + escapeHtml(meta || new Date().toLocaleTimeString()) + '</span></div><div class="msg-body">' + escapeHtml(body || '') + '</div></div>';
      els.chatLog.appendChild(node);
      els.chatLog.scrollTop = els.chatLog.scrollHeight;
      return id;
    }
    function updateBubble(id, body, meta) {
      const node = els.chatLog.querySelector('[data-msg-id="' + CSS.escape(id) + '"]');
      if (!node) return;
      node.querySelector('.msg-body').textContent = body;
      if (meta) node.querySelector('.msg-meta span:last-child').textContent = meta;
      els.chatLog.scrollTop = els.chatLog.scrollHeight;
    }
    function appendTool(title, body) {
      const node = document.createElement('div');
      node.className = 'tool-item';
      node.innerHTML = '<div class="name">' + escapeHtml(title) + '</div><div class="desc">' + escapeHtml(body) + '</div>';
      els.toolFeed.prepend(node);
    }
    function setConnected(connected) {
      state.connected = connected;
      els.sendBtn.disabled = !connected || !els.messageInput.value.trim();
      els.disconnectBtn.disabled = !connected;
      els.connectBtn.disabled = connected;
      const text = connected ? 'Connected' : 'Ready when connected';
      setSocketState(text, connected ? 'ok' : '');
      if (!connected) setStreamState('Disconnected', '');
    }
    function connect() {
      if (state.ws) state.ws.close();
      let socket;
      try {
        socket = new WebSocket(wsURL());
      } catch (error) {
        appendBubble('error', 'Connection Error', String(error));
        setSocketState('Invalid URL', 'err');
        return;
      }
      setSocketState('Connecting', '');
      state.ws = socket;
      socket.addEventListener('open', () => {
        setConnected(true);
        setStreamState('Idle', '');
        appendBubble('meta', 'Socket', 'Connected to ' + wsURL());
      });
      socket.addEventListener('close', () => {
        if (state.ws === socket) state.ws = null;
        setConnected(false);
        state.currentAssistantId = null;
        state.currentAssistantBody = '';
        appendBubble('meta', 'Socket', 'Connection closed');
      });
      socket.addEventListener('error', () => {
        setSocketState('Error', 'err');
        setStreamState('Error', 'err');
        appendBubble('error', 'Socket Error', 'WebSocket connection failed');
      });
      socket.addEventListener('message', event => {
        let payload;
        try {
          payload = JSON.parse(event.data);
        } catch (error) {
          appendTool('parse_error', String(error));
          return;
        }
        handleWSMessage(payload);
      });
    }
    function disconnect() {
      if (state.ws) state.ws.close();
    }
    function sendMessage() {
      if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
        appendBubble('error', 'Send Error', 'WebSocket is not connected');
        return;
      }
      const text = els.messageInput.value.trim();
      if (!text) return;
      appendBubble('user', 'User', text);
      state.currentAssistantBody = '';
      state.currentAssistantId = appendBubble('assistant', 'Assistant', '');
      setStreamState('Streaming', 'ok');
      els.sendBtn.disabled = true;
      state.ws.send(JSON.stringify({ type: 'chat', data: { message: text, stream: true, max_iterations: 8 } }));
      els.messageInput.value = '';
    }
    function handleWSMessage(msg) {
      const data = msg.data || {};
      switch (msg.type) {
        case 'status':
          setStreamState(data.state || 'status', '');
          appendTool('status', JSON.stringify(data, null, 2));
          break;
        case 'reasoning':
          appendTool('reasoning', (data.summary || '') + (data.round ? ' (round ' + data.round + ')' : ''));
          break;
        case 'tool_call':
          appendTool('tool_call: ' + (data.name || 'unknown'), data.display || data.args || JSON.stringify(data.params || {}, null, 2));
          break;
        case 'tool_result':
          appendTool('tool_result: ' + (data.name || 'unknown'), data.display || data.output || '');
          break;
        case 'stream_chunk':
          state.currentAssistantBody += data.content || '';
          if (!state.currentAssistantId) state.currentAssistantId = appendBubble('assistant', 'Assistant', state.currentAssistantBody);
          else updateBubble(state.currentAssistantId, state.currentAssistantBody);
          break;
        case 'stream_end':
          state.currentAssistantBody = data.full_response || state.currentAssistantBody;
          if (!state.currentAssistantId) state.currentAssistantId = appendBubble('assistant', 'Assistant', state.currentAssistantBody);
          else updateBubble(state.currentAssistantId, state.currentAssistantBody, 'done');
          setStreamState('Connected', 'ok');
          state.currentAssistantId = null;
          state.currentAssistantBody = '';
          break;
        case 'error':
          appendBubble('error', 'Agent Error', data.message || 'unknown error');
          appendTool('error', JSON.stringify(data, null, 2));
          setStreamState('Error', 'err');
          state.currentAssistantId = null;
          state.currentAssistantBody = '';
          break;
        default:
          appendTool(msg.type || 'message', JSON.stringify(msg, null, 2));
          break;
      }
    }
    els.refreshBtn.addEventListener('click', refreshData);
    els.connectBtn.addEventListener('click', connect);
    els.disconnectBtn.addEventListener('click', disconnect);
    els.sendBtn.addEventListener('click', sendMessage);
    els.clearBtn.addEventListener('click', () => {
      els.chatLog.innerHTML = '';
      els.toolFeed.innerHTML = '';
      state.currentAssistantId = null;
      state.currentAssistantBody = '';
      setStreamState('Idle', '');
      if (els.heroEmpty) els.heroEmpty.style.display = '';
      const text = state.connected ? 'Connected' : 'Ready when connected';
      setSocketState(text, state.connected ? 'ok' : '');
      els.sendBtn.disabled = !state.connected || !els.messageInput.value.trim();
    });
    els.messageInput.addEventListener('input', () => {
      els.sendBtn.disabled = !state.connected || !els.messageInput.value.trim();
    });
    els.messageInput.addEventListener('keydown', event => {
      if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) sendMessage();
    });
    els.apiBaseInput.addEventListener('input', syncBadges);
    els.sessionInput.addEventListener('input', syncBadges);
    syncBadges();
    refreshData();
    setInterval(refreshData, 5000);
    appendBubble('meta', 'Guide', '1. 填好 API Base 和 Session ID。2. 点击 Connect 建立 WebSocket。3. 输入消息后按 Ctrl+Enter 或 Send 发送。');
  </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

// EnsureDir ensures the dashboard directory exists.
func EnsureDir(path string) error {
	return os.MkdirAll(path, fs.ModeDir|0700)
}
