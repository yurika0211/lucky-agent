package appservice

import (
	"errors"
	"testing"
)

type configurationFixture struct {
	stored, active                               *testConfiguration
	validationErr, saveErr, applyErr, restoreErr error
	writes, applies                              int
}

func (f *configurationFixture) Get() *testConfiguration { return cloneConfiguration(f.stored) }
func (f *configurationFixture) Replace(next *testConfiguration) error {
	f.writes++
	if f.writes == 1 && f.saveErr != nil {
		return f.saveErr
	}
	if f.writes > 1 && f.restoreErr != nil {
		return f.restoreErr
	}
	f.stored = cloneConfiguration(next)
	return nil
}
func (f *configurationFixture) ValidateRuntimeConfig(*testConfiguration) error {
	return f.validationErr
}
func (f *configurationFixture) ApplyRuntimeConfig(next *testConfiguration) error {
	f.applies++
	if f.applies == 1 && f.applyErr != nil {
		return f.applyErr
	}
	f.active = cloneConfiguration(next)
	return nil
}

func TestConfigurationPreviewAndCommit(t *testing.T) {
	current := defaultConfiguration()
	fixture := &configurationFixture{stored: cloneConfiguration(current), active: cloneConfiguration(current)}
	service := NewConfiguration(fixture, fixture, testConfigurationPolicy())
	submitted := cloneConfiguration(current)
	submitted.Iterations = 17
	change, err := service.Prepare(current, submitted)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.writes != 0 || fixture.applies != 0 || fixture.active.Iterations != current.Iterations {
		t.Fatal("preview changed runtime")
	}
	active, err := service.Commit(change)
	if err != nil || active.Iterations != 17 || fixture.active.Iterations != 17 || fixture.writes != 1 {
		t.Fatalf("commit: %v", err)
	}
}

func TestConfigurationFailureStagesAndRollback(t *testing.T) {
	failed := errors.New("injected failure")
	rollbackFailed := errors.New("rollback write failure")
	for _, stage := range []string{"validation", "save", "apply", "rollback"} {
		t.Run(stage, func(t *testing.T) {
			current := defaultConfiguration()
			expectedIterations := current.Iterations
			fixture := &configurationFixture{stored: cloneConfiguration(current), active: cloneConfiguration(current)}
			switch stage {
			case "validation":
				fixture.validationErr = failed
			case "save":
				fixture.saveErr = failed
			case "apply":
				fixture.applyErr = failed
			case "rollback":
				fixture.applyErr = failed
				fixture.restoreErr = rollbackFailed
			}
			service := NewConfiguration(fixture, fixture, testConfigurationPolicy())
			submitted := cloneConfiguration(current)
			submitted.Iterations = 17
			change, err := service.Prepare(current, submitted)
			if stage == "validation" {
				if !errors.Is(err, failed) || ConfigurationErrorStage(err) != "validation" || fixture.writes != 0 {
					t.Fatalf("validation: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			current.Iterations = 99 // caller mutation must not alter the rollback snapshot
			_, err = service.Commit(change)
			if !errors.Is(err, failed) {
				t.Fatalf("failure lost cause: %v", err)
			}
			if stage == "save" {
				if ConfigurationErrorStage(err) != "save" || fixture.applies != 0 || fixture.stored.Iterations != expectedIterations {
					t.Fatal("failed save exposed runtime")
				}
				return
			}
			if fixture.active.Iterations != expectedIterations || fixture.writes != 2 || fixture.applies != 2 {
				t.Fatal("runtime was not restored")
			}
			if stage == "apply" && (!ConfigurationRestored(err) || fixture.stored.Iterations != expectedIterations) {
				t.Fatal("successful rollback not recorded")
			}
			if stage == "rollback" && (ConfigurationRestored(err) || !errors.Is(err, rollbackFailed)) {
				t.Fatal("rollback failure hidden")
			}
		})
	}
}

type testConfiguration struct {
	Iterations int
	Labels     map[string]string
}

func defaultConfiguration() *testConfiguration {
	return &testConfiguration{Iterations: 5, Labels: map[string]string{"project": "original"}}
}
func cloneConfiguration(cfg *testConfiguration) *testConfiguration {
	if cfg == nil {
		return nil
	}
	copy := *cfg
	copy.Labels = make(map[string]string, len(cfg.Labels))
	for key, value := range cfg.Labels {
		copy.Labels[key] = value
	}
	return &copy
}
func testConfigurationPolicy() ConfigurationPolicy[testConfiguration] {
	return ConfigurationPolicy[testConfiguration]{
		Clone:     cloneConfiguration,
		Normalize: func(cfg *testConfiguration) (*testConfiguration, error) { return cloneConfiguration(cfg), nil },
		Classify: func(current, next *testConfiguration) ([]string, []string) {
			if current.Iterations != next.Iterations {
				return []string{"iterations"}, nil
			}
			return nil, nil
		},
	}
}

func TestConfigurationPolicyOwnsPreviewAndReportsNormalizationErrors(t *testing.T) {
	current := defaultConfiguration()
	submitted := cloneConfiguration(current)
	submitted.Iterations = 17
	fixture := &configurationFixture{stored: cloneConfiguration(current), active: cloneConfiguration(current)}
	policy := testConfigurationPolicy()
	policy.Normalize = func(next *testConfiguration) (*testConfiguration, error) {
		next.Labels["normalized"] = "yes"
		return next, nil
	}
	service := NewConfiguration(fixture, fixture, policy)
	change, err := service.Prepare(current, submitted)
	if err != nil || change.Config.Labels["normalized"] != "yes" || submitted.Labels["normalized"] != "" || len(change.HotReloaded) != 1 {
		t.Fatalf("preview ownership/classification: %+v %v", change, err)
	}
	failed := errors.New("normalization failed")
	policy.Normalize = func(*testConfiguration) (*testConfiguration, error) { return nil, failed }
	if _, err := NewConfiguration(fixture, fixture, policy).Prepare(current, submitted); !errors.Is(err, failed) || ConfigurationErrorStage(err) != "validation" || fixture.writes != 0 {
		t.Fatalf("normalization failure: %v", err)
	}
}

func TestConfigurationRejectsUnpreparedOrUnavailableOperations(t *testing.T) {
	var unavailable *configurationFixture
	service := NewConfiguration(unavailable, unavailable, testConfigurationPolicy())
	if _, err := service.Prepare(defaultConfiguration(), defaultConfiguration()); err == nil {
		t.Fatal("nil ports accepted")
	}
	fixture := &configurationFixture{}
	service = NewConfiguration(fixture, fixture, testConfigurationPolicy())
	if _, err := service.Commit(ConfigurationChange[testConfiguration]{Config: defaultConfiguration()}); err == nil || fixture.writes != 0 {
		t.Fatal("unprepared change reached storage")
	}
	if _, err := service.Prepare(nil, defaultConfiguration()); err == nil {
		t.Fatal("nil previous config accepted")
	}
	if _, err := NewConfiguration(fixture, fixture, ConfigurationPolicy[testConfiguration]{}).Prepare(defaultConfiguration(), defaultConfiguration()); err == nil {
		t.Fatal("missing policy accepted")
	}
}
