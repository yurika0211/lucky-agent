package agent

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// executionMetrics holds counts that runtime Metrics has no labels for.
// Restart clears it. Labels are route, result, and token type only.
type executionMetrics struct {
	mu         sync.Mutex
	loopStops  map[string]*atomic.Int64
	saves      map[string]*atomic.Int64
	cron       map[string]*atomic.Int64
	autonomy   map[string]*atomic.Int64
	prompt     atomic.Int64
	completion atomic.Int64
	ttft       *latencyHistogram
}

func newExecutionMetrics() *executionMetrics {
	return &executionMetrics{
		loopStops: map[string]*atomic.Int64{},
		saves:     map[string]*atomic.Int64{},
		cron:      map[string]*atomic.Int64{},
		autonomy:  map[string]*atomic.Int64{},
		ttft:      newLatencyHistogram(defaultLatencyBuckets),
	}
}

func (a *Agent) executionMetrics() *executionMetrics {
	if a == nil {
		return nil
	}
	if a.executionMetricsStore == nil {
		a.executionMetricsOnce.Do(func() {
			if a.executionMetricsStore == nil {
				a.executionMetricsStore = newExecutionMetrics()
			}
		})
	}
	return a.executionMetricsStore
}

func (m *executionMetrics) add(bucket map[string]*atomic.Int64, key string) {
	if m == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "unknown"
	}
	m.mu.Lock()
	counter := bucket[key]
	if counter == nil {
		counter = &atomic.Int64{}
		bucket[key] = counter
	}
	m.mu.Unlock()
	counter.Add(1)
}

func (a *Agent) recordLoopStop(reason string) {
	if a == nil {
		return
	}
	a.executionMetrics().add(a.executionMetrics().loopStops, reason)
}

func (a *Agent) recordSessionSave(err error) {
	if a == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	a.executionMetrics().add(a.executionMetrics().saves, result)
}

func (a *Agent) saveSession(sess *session.Session) error {
	if sess == nil {
		return nil
	}
	err := sess.Save()
	a.recordSessionSave(err)
	return err
}

func saveRuntimeSession(runtime any, sess *session.Session) error {
	if saver, ok := runtime.(interface {
		saveSession(*session.Session) error
	}); ok {
		return saver.saveSession(sess)
	}
	if sess == nil {
		return nil
	}
	return sess.Save()
}

func recordRuntimeLoopStop(runtime any, reason string) {
	if owner, ok := runtime.(interface{ recordLoopStop(string) }); ok {
		owner.recordLoopStop(reason)
	}
}

func recordRuntimeUsage(runtime any, sessionID string, usage *provider.TokenUsage, ttftMS int64) {
	if owner, ok := runtime.(interface {
		recordUsageAndTTFTForSession(string, *provider.TokenUsage, int64)
	}); ok {
		owner.recordUsageAndTTFTForSession(sessionID, usage, ttftMS)
		return
	}
	if owner, ok := runtime.(interface {
		recordUsageAndTTFT(*provider.TokenUsage, int64)
	}); ok {
		owner.recordUsageAndTTFT(usage, ttftMS)
	}
}

func (a *Agent) recordSessionOpen() {
	if a == nil || a.metrics == nil {
		return
	}
	a.metrics.RecordSessionOpen()
}

func (a *Agent) recordProviderChat(ctx context.Context, name string, started time.Time, err error) {
	if a == nil {
		return
	}
	if a.metrics != nil {
		a.metrics.RecordProviderCall(name, time.Since(started), err != nil)
	}
}

// observeProviderStream counts one provider call when the stream ends or fails
// to open. The span stays open until the first terminal read.
func (a *Agent) observeProviderStream(ch <-chan provider.StreamChunk, span trace.Span, name string, started time.Time) <-chan provider.StreamChunk {
	out := make(chan provider.StreamChunk)
	go func() {
		defer close(out)
		defer span.End()
		var streamErr error
		for chunk := range ch {
			if chunk.Err != nil && streamErr == nil {
				streamErr = chunk.Err
			}
			out <- chunk
			if chunk.Done || chunk.Err != nil {
				// Drain so the provider goroutine can exit, but count once.
				for rest := range ch {
					out <- rest
				}
				break
			}
		}
		a.recordProviderChat(context.Background(), name, started, streamErr)
	}()
	return out
}

func (a *Agent) startProviderSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	return telemetry.StartSpan(ctx, "provider.chat")
}

func (a *Agent) recordUsageAndTTFT(usage *provider.TokenUsage, ttftMS int64) {
	if a == nil {
		return
	}
	sessionID := ""
	if usage != nil && (usage.InputTokens > 0 || usage.TotalTokens > 0) {
		sessionID = a.activeCompactSessionID()
	}
	a.recordUsageAndTTFTForSession(sessionID, usage, ttftMS)
}

func (a *Agent) recordUsageAndTTFTForSession(sessionID string, usage *provider.TokenUsage, ttftMS int64) {
	if a == nil {
		return
	}
	if sessionID != "" {
		a.recordMeasuredPromptUsage(sessionID, usage)
	}
	m := a.executionMetrics()
	if usage != nil {
		if usage.InputTokens > 0 {
			m.prompt.Add(int64(usage.InputTokens))
		}
		if usage.OutputTokens > 0 {
			m.completion.Add(int64(usage.OutputTokens))
		}
	}
	if ttftMS > 0 {
		m.ttft.observe(time.Duration(ttftMS) * time.Millisecond)
	}
}

func (a *Agent) recordCronResult(result string) {
	if a == nil {
		return
	}
	a.executionMetrics().add(a.executionMetrics().cron, result)
}

func (a *Agent) recordAutonomyResult(result string) {
	if a == nil {
		return
	}
	a.executionMetrics().add(a.executionMetrics().autonomy, result)
}

func loopStopReason(err error, state string) string {
	if err == nil {
		return "complete"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "max iterations"):
		return "max_iterations"
	case strings.Contains(text, "memory gate"):
		return "memory_gate"
	case strings.Contains(text, "context deadline") || strings.Contains(text, "timeout") || strings.Contains(text, "timed out"):
		return "timeout"
	case strings.Contains(text, "context canceled") || strings.Contains(text, "context cancelled"):
		return "canceled"
	default:
		if strings.EqualFold(state, "done") && strings.Contains(text, "incomplete") {
			return "error"
		}
		return "error"
	}
}

func streamRecordsChatAtEntry(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "http", "http-sse", "http_sse", "https":
		return true
	default:
		return false
	}
}

func (m *executionMetrics) exportPrometheus() string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	writeLabeled := func(help, typ, metric string, bucket map[string]*atomic.Int64, label string) {
		m.mu.Lock()
		keys := make([]string, 0, len(bucket))
		copied := make(map[string]int64, len(bucket))
		for key, counter := range bucket {
			keys = append(keys, key)
			copied[key] = counter.Load()
		}
		m.mu.Unlock()
		if len(keys) == 0 {
			return
		}
		sort.Strings(keys)
		b.WriteString("# HELP " + help + "\n")
		b.WriteString("# TYPE " + metric + " " + typ + "\n")
		for _, key := range keys {
			b.WriteString(metric + `{` + label + `="` + promLabel(key) + `"} ` + formatMetricInt(copied[key]) + "\n")
		}
	}
	writeLabeled("lh_loop_stop_total Loop stops by reason.", "counter", "lh_loop_stop_total", m.loopStops, "reason")
	writeLabeled("lh_session_save_total Session saves by result.", "counter", "lh_session_save_total", m.saves, "result")
	writeLabeled("lh_cron_jobs_total Cron runs by result.", "counter", "lh_cron_jobs_total", m.cron, "result")
	writeLabeled("lh_autonomy_tasks_total Autonomy worker notifications by result.", "counter", "lh_autonomy_tasks_total", m.autonomy, "result")
	if prompt, completion := m.prompt.Load(), m.completion.Load(); prompt > 0 || completion > 0 {
		b.WriteString("# HELP lh_provider_tokens_total Provider tokens by type.\n")
		b.WriteString("# TYPE lh_provider_tokens_total counter\n")
		b.WriteString(`lh_provider_tokens_total{type="prompt"} ` + formatMetricInt(prompt) + "\n")
		b.WriteString(`lh_provider_tokens_total{type="completion"} ` + formatMetricInt(completion) + "\n")
	}
	if m.ttft != nil && m.ttft.total.Load() > 0 {
		b.WriteString("# HELP lh_chat_ttft_seconds Time to first token.\n")
		b.WriteString("# TYPE lh_chat_ttft_seconds histogram\n")
		var count int64
		for i := range m.ttft.buckets {
			le := "+Inf"
			if bound := m.ttft.buckets[i].upperBound; bound > 0 {
				le = strconv.FormatFloat(bound.Seconds(), 'f', -1, 64)
			} else {
				count = m.ttft.buckets[i].count.Load()
			}
			b.WriteString(`lh_chat_ttft_seconds_bucket{le="` + le + `"} ` + formatMetricInt(m.ttft.buckets[i].count.Load()) + "\n")
		}
		sum := time.Duration(m.ttft.sum.Load()).Seconds()
		b.WriteString("lh_chat_ttft_seconds_sum " + strconv.FormatFloat(sum, 'f', 6, 64) + "\n")
		b.WriteString(`lh_chat_ttft_seconds_count ` + formatMetricInt(count) + "\n")
	}
	return b.String()
}
