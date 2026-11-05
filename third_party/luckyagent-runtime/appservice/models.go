package appservice

import (
	"context"
	"fmt"
	"strings"
)

// ModelRuntime supplies model selection without chat execution or transport.
type ModelRuntime interface {
	ListModels(*ModelKind) []ModelRef
	SwitchModelKind(ModelKind, string, SwitchModelOptions) error
}

type ModelQuery struct {
	Kind     *ModelKind
	Provider string
	Refresh  bool
}

type ModelList struct {
	Models         []ModelRef
	Source         string
	DiscoveryError error
}

type Models struct{ runtime ModelRuntime }

func NewModels(runtime ModelRuntime) *Models { return &Models{runtime: runtime} }

// List never filters a runtime-owned slice in place. Discovery failure retains
// the catalog and is returned separately so each entrance can render its policy.
func (s *Models) List(ctx context.Context, query ModelQuery) ModelList {
	result := ModelList{Models: make([]ModelRef, 0), Source: "catalog"}
	if s == nil || absent(s.runtime) {
		result.DiscoveryError = fmt.Errorf("model runtime is unavailable")
		return result
	}
	models := append([]ModelRef(nil), s.runtime.ListModels(query.Kind)...)
	if query.Refresh {
		if discovery, ok := s.runtime.(interface {
			RefreshChatModels(context.Context) ([]ModelRef, error)
			CompactModelsFromChatDiscovery([]ModelRef) []ModelRef
		}); ok {
			fresh, err := discovery.RefreshChatModels(ctx)
			result.DiscoveryError = err
			if err == nil {
				models = append([]ModelRef(nil), s.runtime.ListModels(query.Kind)...)
				if query.Kind == nil || *query.Kind == ModelKindChat {
					kept := make([]ModelRef, 0, len(models)+len(fresh))
					for _, model := range models {
						if model.Kind != ModelKindChat && model.Kind != ModelKindCompact {
							kept = append(kept, model)
						}
					}
					models = append(kept, fresh...)
					models = append(models, discovery.CompactModelsFromChatDiscovery(fresh)...)
				}
				result.Source = "provider"
			}
		} else {
			result.DiscoveryError = fmt.Errorf("model discovery is unavailable")
		}
	}
	providerName := strings.TrimSpace(query.Provider)
	for _, model := range models {
		if providerName == "" || strings.EqualFold(model.Provider, providerName) {
			model.Capabilities = append([]string(nil), model.Capabilities...)
			result.Models = append(result.Models, model)
		}
	}
	return result
}

func (s *Models) Switch(kind ModelKind, id string, opts SwitchModelOptions) error {
	if s == nil || absent(s.runtime) {
		return fmt.Errorf("model runtime is unavailable")
	}
	return s.runtime.SwitchModelKind(kind, strings.TrimSpace(id), opts)
}

type LegacyModelRuntime interface {
	ResolveModelProvider(string) (string, bool)
	SwitchModel(string) error
}

// ModelSwitchError identifies partial legacy failures without embedding a
// platform-specific warning. Modern runtimes use SwitchModelKind directly.
type ModelSwitchError struct {
	Stage string // runtime, provider, model, save
	Err   error
}

func (e *ModelSwitchError) Error() string { return e.Err.Error() }
func (e *ModelSwitchError) Unwrap() error { return e.Err }

func SwitchLegacyModel(runtime LegacyModelRuntime, settings MutableSettings, id string) (persisted bool, err error) {
	if absent(runtime) {
		return false, &ModelSwitchError{Stage: "runtime", Err: fmt.Errorf("model runtime is unavailable")}
	}
	id = strings.TrimSpace(id)
	providerName, _ := runtime.ResolveModelProvider(id)
	if err := runtime.SwitchModel(id); err != nil {
		return false, &ModelSwitchError{Stage: "runtime", Err: err}
	}
	if absent(settings) {
		return false, nil
	}
	var changes []Setting
	if providerName != "" {
		changes = append(changes, Setting{Key: "provider", Value: providerName})
	}
	changes = append(changes, Setting{Key: "model", Value: id})
	if err := SaveSettings(settings, changes...); err != nil {
		failure := err.(*SettingError)
		stage := failure.Key
		if stage == "" {
			stage = "save"
		}
		return false, &ModelSwitchError{Stage: stage, Err: failure.Err}
	}
	return true, nil
}
