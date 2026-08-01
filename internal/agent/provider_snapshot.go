package agent

import (
	"strings"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/provider"
)

// providerSnapshot is the immutable provider selection for one turn.
//
// Agent.provider is the process-wide default selected by configuration or an
// explicit SwitchModel call.  A model router must not replace that pointer
// while another turn is using it: doing so makes the provider used by a turn
// depend on scheduling.  A snapshot keeps the provider and the metadata that
// describes it together for the whole turn.
type providerSnapshot struct {
	provider provider.Provider
	model    string
	apiBase  string
}

func (s providerSnapshot) valid() bool {
	return s.provider != nil
}

func (s providerSnapshot) name() string {
	if s.provider == nil {
		return ""
	}
	return s.provider.Name()
}

// providerSnapshotForTurn takes a stable copy of the current default provider
// and, when enabled, resolves a routed provider without mutating Agent.  The
// returned value must be passed down the loop/stream call chain and treated as
// immutable.
func (a *Agent) providerSnapshotForTurn(userInput string) providerSnapshot {
	if a == nil {
		return providerSnapshot{}
	}

	base := a.baseProviderSnapshot()
	if a.cfg == nil || a.registry == nil || base.provider == nil {
		return base
	}

	cfg := a.cfg.Get()
	if cfg == nil || !cfg.ModelRouter.Enable || len(cfg.Fallbacks) > 0 {
		return base
	}

	tokenCount := len(userInput) / 4
	if a.contextEst != nil {
		tokenCount = a.contextEst.Estimate(userInput)
	}
	router := config.NewModelRouter(cfg.ModelRouter)
	model, apiBase := router.SelectModelForTask(userInput, tokenCount)
	model = strings.TrimSpace(model)
	if model == "" {
		return base
	}

	effectiveAPIBase := strings.TrimSpace(apiBase)
	if effectiveAPIBase == "" {
		effectiveAPIBase = strings.TrimSpace(cfg.APIBase)
	}
	if model == strings.TrimSpace(base.model) && effectiveAPIBase == strings.TrimSpace(base.apiBase) {
		return base
	}

	pCfg := toProviderConfig(cfg, model, apiBase)
	// Registry is not internally synchronized.  Serialize route-provider
	// creation with SwitchModel and other route resolutions.  The returned
	// provider itself is immutable configuration and can safely outlive this
	// lock for the duration of the turn.
	a.providerMu.Lock()
	routedProvider, err := a.registry.Create(pCfg.LlmProvider.Name, pCfg)
	a.providerMu.Unlock()
	if err != nil || routedProvider == nil {
		return base
	}

	return providerSnapshot{
		provider: wrapProviderWithMiddleware(routedProvider, cfg),
		model:    model,
		apiBase:  effectiveAPIBase,
	}
}

// baseProviderSnapshot returns the process-wide default provider state.  It is
// intentionally separate from providerSnapshotForTurn so callers that need a
// background/default operation do not accidentally perform model routing.
func (a *Agent) baseProviderSnapshot() providerSnapshot {
	if a == nil {
		return providerSnapshot{}
	}
	a.providerMu.RLock()
	snapshot := providerSnapshot{
		provider: a.provider,
		model:    a.activeModel,
		apiBase:  a.activeAPIBase,
	}
	a.providerMu.RUnlock()
	return snapshot
}

// maybeRouteModel is kept as a compatibility shim for package-local callers.
// It no longer mutates Agent; callers should retain the returned snapshot.
func (a *Agent) maybeRouteModel(userInput string) providerSnapshot {
	return a.providerSnapshotForTurn(userInput)
}
