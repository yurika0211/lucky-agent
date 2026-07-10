package feishu

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

const maxCallbackBodyBytes = 1 << 20

// ErrUnsupportedMedia reports the Phase-1 limitation for outbound media.
var ErrUnsupportedMedia = errors.New("feishu: outbound media is not supported")

// Adapter implements gateway.Gateway using Feishu event callbacks and APIs.
type Adapter struct {
	cfg        Config
	httpClient *http.Client
	now        func() time.Time

	mu        sync.RWMutex
	handler   gateway.MessageHandler
	running   bool
	cancel    context.CancelFunc
	server    *http.Server
	listener  net.Listener
	serveDone chan struct{}
	runCtx    context.Context
	botOpenID string

	identityMu  sync.Mutex
	tokenMu     sync.Mutex
	accessToken string
	tokenExpiry time.Time

	outboundMessages *expiringIDSet
	receivedEvents   *expiringIDSet
}

// NewAdapter creates a Feishu gateway adapter.
func NewAdapter(cfg Config) *Adapter {
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		cfg.ListenAddr = defaultListenAddr
	}
	if strings.TrimSpace(cfg.Path) == "" {
		cfg.Path = defaultPath
	}
	if strings.TrimSpace(cfg.APIBaseURL) == "" {
		cfg.APIBaseURL = defaultAPIBaseURL
	}
	if strings.TrimSpace(cfg.GroupTriggerMode) == "" {
		cfg.GroupTriggerMode = "mention"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Adapter{
		cfg:              cfg,
		httpClient:       client,
		now:              time.Now,
		botOpenID:        strings.TrimSpace(cfg.BotOpenID),
		outboundMessages: newExpiringIDSet(outboundMessageTTL, maxTrackedIDs),
		receivedEvents:   newExpiringIDSet(eventDedupTTL, maxTrackedIDs),
	}
}

func (a *Adapter) Name() string { return "feishu" }

// SetHandler registers the incoming message handler.
func (a *Adapter) SetHandler(handler gateway.MessageHandler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handler = handler
}

// Start starts the Feishu event callback HTTP server.
func (a *Adapter) Start(ctx context.Context) error {
	if strings.TrimSpace(a.cfg.AppID) == "" || strings.TrimSpace(a.cfg.AppSecret) == "" {
		return fmt.Errorf("feishu: app_id and app_secret are required")
	}
	if strings.TrimSpace(a.cfg.VerificationToken) == "" {
		return fmt.Errorf("feishu: verification_token is required")
	}
	if strings.TrimSpace(a.cfg.EncryptKey) != "" {
		return fmt.Errorf("feishu: encrypted event callbacks are not supported")
	}
	if err := a.resolveBotOpenID(ctx); err != nil {
		return fmt.Errorf("feishu: resolve bot identity: %w", err)
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}
	ln, err := net.Listen("tcp", a.cfg.normalizedListenAddr())
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("feishu: listen %s: %w", a.cfg.normalizedListenAddr(), err)
	}

	startCtx, cancel := context.WithCancel(ctx)
	mux := http.NewServeMux()
	mux.HandleFunc(a.cfg.normalizedPath(), a.handleCallback)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	done := make(chan struct{})
	a.cancel = cancel
	a.server = server
	a.listener = ln
	a.serveDone = done
	a.runCtx = startCtx
	a.running = true
	a.mu.Unlock()

	go a.serve(server, ln, done)
	go func() {
		<-startCtx.Done()
		_ = a.stopServer(server)
	}()
	return nil
}

func (a *Adapter) serve(server *http.Server, ln net.Listener, done chan struct{}) {
	defer close(done)
	err := server.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("[feishu] callback server stopped: %v", err)
	}
	a.mu.Lock()
	if a.server == server {
		cancel := a.cancel
		a.running = false
		a.server = nil
		a.listener = nil
		a.cancel = nil
		a.runCtx = nil
		a.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return
	}
	a.mu.Unlock()
}

// Stop gracefully stops the callback server.
func (a *Adapter) Stop() error {
	a.mu.RLock()
	server := a.server
	a.mu.RUnlock()
	if server == nil {
		return nil
	}
	return a.stopServer(server)
}

func (a *Adapter) stopServer(server *http.Server) error {
	a.mu.Lock()
	if a.server != server {
		a.mu.Unlock()
		return nil
	}
	cancel := a.cancel
	done := a.serveDone
	a.running = false
	a.cancel = nil
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := server.Shutdown(shutdownCtx)
	shutdownCancel()
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("feishu: shutdown callback server: %w", err)
	}
	return nil
}

func (a *Adapter) IsRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.running
}

// ListenAddr returns the active listener address, including the selected port
// when ListenAddr was configured with port 0.
func (a *Adapter) ListenAddr() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.listener != nil {
		return a.listener.Addr().String()
	}
	return a.cfg.normalizedListenAddr()
}

// Path returns the normalized callback path.
func (a *Adapter) Path() string { return a.cfg.normalizedPath() }

func (a *Adapter) handleCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "gateway": a.Name()})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": http.StatusMethodNotAllowed, "msg": "method not allowed"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCallbackBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": http.StatusBadRequest, "msg": "invalid request body"})
		return
	}
	var envelope callbackEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": http.StatusBadRequest, "msg": "invalid JSON"})
		return
	}
	if strings.TrimSpace(envelope.Encrypt) != "" {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"code": http.StatusNotImplemented, "msg": "encrypted callbacks are not supported"})
		return
	}

	providedToken := envelope.Token
	if strings.TrimSpace(providedToken) == "" {
		providedToken = envelope.Header.Token
	}
	if !secureTokenEqual(providedToken, a.cfg.VerificationToken) {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": http.StatusForbidden, "msg": "verification token mismatch"})
		return
	}
	if envelope.Type == "url_verification" || envelope.Challenge != "" {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": envelope.Challenge})
		return
	}
	if strings.TrimSpace(envelope.Header.AppID) != "" && !secureTokenEqual(envelope.Header.AppID, a.cfg.AppID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": http.StatusForbidden, "msg": "app id mismatch"})
		return
	}
	if envelope.Header.EventType != "im.message.receive_v1" {
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}
	if envelope.Schema != "2.0" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": http.StatusBadRequest, "msg": "only schema 2.0 callbacks are supported"})
		return
	}

	msg, err := a.convertEvent(envelope.Event)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": http.StatusBadRequest, "msg": err.Error()})
		return
	}
	if msg == nil {
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}
	if eventID := strings.TrimSpace(envelope.Header.EventID); eventID != "" && a.receivedEvents.seenOrAdd(eventID, a.now()) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}
	a.mu.RLock()
	handler := a.handler
	handlerCtx := a.runCtx
	a.mu.RUnlock()
	if handler != nil {
		if handlerCtx == nil {
			handlerCtx = context.Background()
		}
		go func() {
			if err := handler(handlerCtx, msg); err != nil {
				log.Printf("[feishu] message handler failed: %v", err)
			}
		}()
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": 0})
}

func secureTokenEqual(got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if got == "" || want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (a *Adapter) SendPhoto(context.Context, string, string, string, string) error {
	return fmt.Errorf("%w in phase 1", ErrUnsupportedMedia)
}

func (a *Adapter) SendDocument(context.Context, string, string, string, string) error {
	return fmt.Errorf("%w in phase 1", ErrUnsupportedMedia)
}

var _ gateway.Gateway = (*Adapter)(nil)
