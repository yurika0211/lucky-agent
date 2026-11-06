package agent

import (
	"encoding/json"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/config"
)

// providerSnapshotForProfile applies the lightweight profile to the primary
// request provider while preserving the configured fallback nodes. The final
// fallback can therefore still override reasoning_effort with none.
func (a *Agent) providerSnapshotForProfile(snapshot providerSnapshot, profile RequestProfile) providerSnapshot {
	return a.providerSnapshotForProfileSource(snapshot, profile, "")
}

func (a *Agent) providerSnapshotForProfileSource(snapshot providerSnapshot, profile RequestProfile, source string) providerSnapshot {
	if a == nil || a.cfg == nil || a.registry == nil || !snapshot.valid() {
		return snapshot
	}
	if profile != RequestProfileChat && profile != RequestProfileToolTask {
		return snapshot
	}

	current := a.cfg.Get()
	// Keep injected/test providers intact. Rebuilding from config here would
	// silently replace a caller-provided provider (and can turn a local test
	// double into a real network call). Production providers are rebuilt only
	// when the snapshot matches the configured provider kind.
	configuredName := strings.TrimSpace(current.LlmProvider.Name)
	if configuredName != "" && !strings.EqualFold(strings.TrimSpace(snapshot.provider.Name()), configuredName) {
		return snapshot
	}
	data, err := json.Marshal(current)
	if err != nil {
		return snapshot
	}
	var tuned config.Config
	if err := json.Unmarshal(data, &tuned); err != nil {
		return snapshot
	}
	tuned.Provider, tuned.APIKey, tuned.APIBase, tuned.Model = current.Provider, current.APIKey, current.APIBase, current.Model
	effort := "medium"
	if profile == RequestProfileChat && (source == "explicit_chat" || source == "empty_input") {
		effort = "low"
	}
	tuned.LlmProvider.ReasoningEffort = effort
	if tuned.Models.Endpoints != nil {
		endpoint := tuned.Models.Endpoints[config.ModelKindChat]
		endpoint.ReasoningEffort = effort
		tuned.Models.Endpoints[config.ModelKindChat] = endpoint
	}
	p, err := buildConfiguredProvider(&tuned, a.registry)
	if err != nil || p == nil {
		return snapshot
	}
	return providerSnapshot{
		provider: wrapProviderWithMiddleware(p, current),
		model:    snapshot.model,
		apiBase:  snapshot.apiBase,
	}
}
