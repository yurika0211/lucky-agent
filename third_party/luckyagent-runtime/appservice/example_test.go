package appservice_test

import (
	"fmt"

	"github.com/yurika0211/luckyagent-runtime/appservice"
)

// Applications keep their own schema and supply storage and runtime ports.
type exampleConfiguration struct{ Limit int }
type exampleRuntime struct{ stored, active *exampleConfiguration }

func cloneExampleConfig(cfg *exampleConfiguration) *exampleConfiguration {
	copy := *cfg
	return &copy
}
func (r *exampleRuntime) Get() *exampleConfiguration { return cloneExampleConfig(r.stored) }
func (r *exampleRuntime) Replace(cfg *exampleConfiguration) error {
	r.stored = cloneExampleConfig(cfg)
	return nil
}
func (*exampleRuntime) ValidateRuntimeConfig(cfg *exampleConfiguration) error {
	if cfg.Limit <= 0 {
		return fmt.Errorf("limit must be positive")
	}
	return nil
}
func (r *exampleRuntime) ApplyRuntimeConfig(cfg *exampleConfiguration) error {
	r.active = cloneExampleConfig(cfg)
	return nil
}

func ExampleNewConfiguration() {
	current := &exampleConfiguration{Limit: 10}
	runtime := &exampleRuntime{stored: current, active: current}
	service := appservice.NewConfiguration(runtime, runtime, appservice.ConfigurationPolicy[exampleConfiguration]{
		Clone: cloneExampleConfig,
		Normalize: func(cfg *exampleConfiguration) (*exampleConfiguration, error) {
			return cfg, nil
		},
		Classify: func(current, next *exampleConfiguration) ([]string, []string) {
			if current.Limit != next.Limit {
				return []string{"limit"}, nil
			}
			return nil, nil
		},
	})
	change, err := service.Prepare(current, &exampleConfiguration{Limit: 20})
	if err != nil {
		panic(err)
	}
	active, err := service.Commit(change)
	if err != nil {
		panic(err)
	}
	fmt.Println(active.Limit, runtime.active.Limit)
	// Output: 20 20
}
