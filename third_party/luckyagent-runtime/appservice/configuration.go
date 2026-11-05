package appservice

import (
	"errors"
	"fmt"
)

type ConfigurationRuntime[T any] interface {
	ValidateRuntimeConfig(*T) error
	ApplyRuntimeConfig(*T) error
}

// ConfigurationStore returns caller-safe snapshots. Replace must keep the
// previous configuration intact when it returns an error.
type ConfigurationStore[T any] interface {
	Get() *T
	Replace(*T) error
}

// ConfigurationPolicy supplies application-specific schema rules. Clone must
// make an independent snapshot, including mutable maps and slices.
type ConfigurationPolicy[T any] struct {
	Clone     func(*T) *T
	Normalize func(*T) (*T, error)
	Classify  func(current, next *T) (hotReloaded, restartRequired []string)
}

type Configuration[T any] struct {
	store   ConfigurationStore[T]
	runtime ConfigurationRuntime[T]
	policy  ConfigurationPolicy[T]
}

func NewConfiguration[T any](store ConfigurationStore[T], runtime ConfigurationRuntime[T], policy ConfigurationPolicy[T]) *Configuration[T] {
	return &Configuration[T]{store: store, runtime: runtime, policy: policy}
}

type ConfigurationError struct {
	Stage         string // validation, save, apply
	Err           error
	RollbackError error
}

func (e *ConfigurationError) Error() string { return e.Err.Error() }
func (e *ConfigurationError) Unwrap() error { return e.Err }

func ConfigurationErrorStage(err error) string {
	var failure *ConfigurationError
	if errors.As(err, &failure) {
		return failure.Stage
	}
	return "validation"
}

func ConfigurationRestored(err error) bool {
	var failure *ConfigurationError
	return errors.As(err, &failure) && failure.Stage == "apply" && failure.RollbackError == nil
}

// ConfigurationChange keeps rollback state private. Use Prepare to construct it.
type ConfigurationChange[T any] struct {
	Config          *T
	HotReloaded     []string
	RestartRequired []string
	previous        *T
}

// Prepare validates and normalizes a private copy. Preview has no storage or
// runtime side effects. Callers serialize their complete read/prepare/commit.
func (s *Configuration[T]) Prepare(current, submitted *T) (ConfigurationChange[T], error) {
	invalid := func(err error) (ConfigurationChange[T], error) {
		return ConfigurationChange[T]{}, &ConfigurationError{Stage: "validation", Err: err}
	}
	if s == nil || absent(s.runtime) || absent(s.store) {
		return invalid(fmt.Errorf("configuration service is unavailable"))
	}
	if current == nil || submitted == nil {
		return invalid(fmt.Errorf("current and submitted configurations are required"))
	}
	if s.policy.Clone == nil || s.policy.Normalize == nil || s.policy.Classify == nil {
		return invalid(fmt.Errorf("configuration clone, normalize and classify policies are required"))
	}
	next := s.policy.Clone(submitted)
	if next == nil {
		return invalid(fmt.Errorf("configuration clone returned nil"))
	}
	if err := s.runtime.ValidateRuntimeConfig(next); err != nil {
		return invalid(err)
	}
	normalized, err := s.policy.Normalize(next)
	if err != nil {
		return invalid(err)
	}
	if normalized == nil {
		return invalid(fmt.Errorf("configuration normalization returned nil"))
	}
	previous := s.policy.Clone(current)
	if previous == nil {
		return invalid(fmt.Errorf("configuration snapshot returned nil"))
	}
	hot, restart := s.policy.Classify(current, normalized)
	return ConfigurationChange[T]{Config: normalized, HotReloaded: hot, RestartRequired: restart, previous: previous}, nil
}

// Commit persists before applying. Apply failure attempts both storage and
// runtime restoration, exposing either rollback failure through errors.Is/As.
func (s *Configuration[T]) Commit(change ConfigurationChange[T]) (*T, error) {
	if s == nil || absent(s.store) || absent(s.runtime) || s.policy.Clone == nil || change.Config == nil || change.previous == nil {
		return nil, &ConfigurationError{Stage: "validation", Err: fmt.Errorf("configuration change was not prepared")}
	}
	if err := s.store.Replace(change.Config); err != nil {
		return nil, &ConfigurationError{Stage: "save", Err: err}
	}
	if err := s.runtime.ApplyRuntimeConfig(s.store.Get()); err != nil {
		restoreStore := s.store.Replace(s.policy.Clone(change.previous))
		restoreRuntime := s.runtime.ApplyRuntimeConfig(s.policy.Clone(change.previous))
		return nil, &ConfigurationError{Stage: "apply", Err: errors.Join(err, restoreStore, restoreRuntime), RollbackError: errors.Join(restoreStore, restoreRuntime)}
	}
	return s.store.Get(), nil
}

type MutableSettings interface {
	Set(string, string) error
	Save() error
}

type Setting struct{ Key, Value string }

type SettingError struct {
	Key string
	Err error
}

func (e *SettingError) Error() string { return e.Err.Error() }
func (e *SettingError) Unwrap() error { return e.Err }

// SaveSettings preserves update order and saves only after every Set succeeds.
// Legacy settings ports do not provide transactional rollback.
func SaveSettings(store MutableSettings, changes ...Setting) error {
	if absent(store) {
		return &SettingError{Err: fmt.Errorf("settings store is unavailable")}
	}
	for _, change := range changes {
		if err := store.Set(change.Key, change.Value); err != nil {
			return &SettingError{Key: change.Key, Err: err}
		}
	}
	if err := store.Save(); err != nil {
		return &SettingError{Err: err}
	}
	return nil
}
