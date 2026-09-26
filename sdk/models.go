package sdk

import (
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
)

// ModelInfo is a credential-free model description for host UIs.
type ModelInfo struct {
	ID           string
	Kind         string
	Provider     string
	DisplayName  string
	APIBase      string
	Protocol     string
	Capabilities []string
	Current      bool
}

// CurrentModel returns the active chat model selection.
func (a *Agent) CurrentModel() (ModelInfo, bool) {
	if err := a.require(); err != nil {
		return ModelInfo{}, false
	}
	ref, ok := a.inner.CurrentModel(config.ModelKindChat)
	if !ok {
		return ModelInfo{}, false
	}
	return mapModelRef(ref), true
}

// ListModels returns catalog + currently selected models for host pickers.
//
// kind filters by purpose when non-empty (chat, vision, embedding, transcription,
// image, tts, reranker). Empty kind returns all kinds the runtime knows about.
func (a *Agent) ListModels(kind string) ([]ModelInfo, error) {
	if err := a.require(); err != nil {
		return nil, err
	}
	kind = strings.TrimSpace(kind)
	var filter *config.ModelKind
	if kind != "" {
		parsed, err := config.ParseModelKind(kind)
		if err != nil {
			return nil, fmt.Errorf("sdk: %w", err)
		}
		filter = &parsed
	}
	refs := a.inner.ListModels(filter)
	out := make([]ModelInfo, 0, len(refs))
	for _, ref := range refs {
		out = append(out, mapModelRef(ref))
	}
	return out, nil
}

func mapModelRef(ref agent.ModelRef) ModelInfo {
	caps := ref.Capabilities
	if caps != nil {
		caps = append([]string(nil), caps...)
	}
	return ModelInfo{
		ID:           ref.ID,
		Kind:         string(ref.Kind),
		Provider:     ref.Provider,
		DisplayName:  ref.DisplayName,
		APIBase:      ref.APIBase,
		Protocol:     ref.Protocol,
		Capabilities: caps,
		Current:      ref.Current,
	}
}
