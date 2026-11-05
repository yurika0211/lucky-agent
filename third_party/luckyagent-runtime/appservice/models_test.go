package appservice

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type modelFixture struct {
	models, fresh []ModelRef
	discoveryErr  error
}

func (f *modelFixture) ListModels(*ModelKind) []ModelRef { return f.models }
func (*modelFixture) CurrentModel(ModelKind) (ModelRef, bool) {
	return ModelRef{}, false
}
func (*modelFixture) SwitchModelKind(ModelKind, string, SwitchModelOptions) error {
	return nil
}
func (f *modelFixture) RefreshChatModels(ctx context.Context) ([]ModelRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.fresh, f.discoveryErr
}
func (*modelFixture) CompactModelsFromChatDiscovery(fresh []ModelRef) []ModelRef {
	var result []ModelRef
	for _, model := range fresh {
		model.Kind = ModelKindCompact
		result = append(result, model)
	}
	return result
}

func TestModelFilteringOwnsReturnedSlices(t *testing.T) {
	fixture := &modelFixture{models: []ModelRef{
		{ID: "one", Provider: "openai", Capabilities: []string{"vision"}},
		{ID: "two", Provider: "ollama"},
	}}
	result := NewModels(fixture).List(context.Background(), ModelQuery{Provider: " OLLAMA "})
	if len(result.Models) != 1 || result.Models[0].ID != "two" || fixture.models[0].ID != "one" {
		t.Fatalf("filter mutated runtime: %+v", fixture.models)
	}
	result = NewModels(fixture).List(context.Background(), ModelQuery{})
	result.Models[0].Capabilities[0] = "changed"
	if fixture.models[0].Capabilities[0] != "vision" {
		t.Fatal("capabilities alias runtime state")
	}
}

func TestModelRefreshKeepsNonChatModelsAndReportsFailure(t *testing.T) {
	fixture := &modelFixture{
		models: []ModelRef{{ID: "old", Kind: ModelKindChat}, {ID: "old-compact", Kind: ModelKindCompact}, {ID: "embed", Kind: ModelKindEmbedding}},
		fresh:  []ModelRef{{ID: "new", Kind: ModelKindChat}},
	}
	result := NewModels(fixture).List(context.Background(), ModelQuery{Refresh: true})
	if result.Source != "provider" || len(result.Models) != 3 || result.Models[0].ID != "embed" || result.Models[1].ID != "new" || result.Models[2].Kind != ModelKindCompact {
		t.Fatalf("refresh = %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = NewModels(fixture).List(ctx, ModelQuery{Refresh: true})
	if result.Source != "catalog" || !errors.Is(result.DiscoveryError, context.Canceled) || result.Models[0].ID != "old" {
		t.Fatalf("cancel = %+v", result)
	}
}

type legacyFixture struct {
	provider  string
	switchErr error
}

func (f legacyFixture) ResolveModelProvider(string) (string, bool) {
	return f.provider, f.provider != ""
}
func (f legacyFixture) SwitchModel(string) error { return f.switchErr }

type settingsFixture struct {
	updates []string
	failKey string
	saveErr error
	saved   bool
}

func (f *settingsFixture) Set(key, value string) error {
	f.updates = append(f.updates, key)
	if key == f.failKey {
		return errors.New("write rejected")
	}
	return nil
}
func (f *settingsFixture) Save() error { f.saved = true; return f.saveErr }

func TestLegacySwitchDistinguishesRuntimeAndPersistenceFailures(t *testing.T) {
	for _, test := range []struct {
		name, key, stage    string
		runtimeErr, saveErr error
	}{
		{name: "runtime", runtimeErr: errors.New("switch rejected"), stage: "runtime"},
		{name: "provider", key: "provider", stage: "provider"},
		{name: "model", key: "model", stage: "model"},
		{name: "save", saveErr: errors.New("disk full"), stage: "save"},
		{name: "success"},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings := &settingsFixture{failKey: test.key, saveErr: test.saveErr}
			persisted, err := SwitchLegacyModel(legacyFixture{provider: "openai", switchErr: test.runtimeErr}, settings, "fixture-model")
			if test.stage == "" {
				if err != nil || !persisted || !reflect.DeepEqual(settings.updates, []string{"provider", "model"}) {
					t.Fatalf("success: %v %v %+v", persisted, err, settings)
				}
				return
			}
			var failure *ModelSwitchError
			if !errors.As(err, &failure) || failure.Stage != test.stage || persisted {
				t.Fatalf("failure: %v %v", persisted, err)
			}
			if test.stage == "runtime" && len(settings.updates) != 0 {
				t.Fatal("runtime failure wrote settings")
			}
			if (test.stage == "provider" || test.stage == "model") && settings.saved {
				t.Fatal("partial update saved")
			}
		})
	}
}
