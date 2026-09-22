package config

// ForegroundConfig bounds work that stays attached to the user's request.
// It never authorizes a detached worker or automatic startup recovery.
type ForegroundConfig struct {
	Enabled         *bool `json:"enabled,omitempty"`
	MaxSlices       int   `json:"max_slices,omitempty"`
	MaxTotalSeconds int   `json:"max_total_seconds,omitempty"`
	MaxRetries      *int  `json:"max_retries,omitempty"`
}

func DefaultForegroundConfig() ForegroundConfig {
	enabled, retries := true, 3
	return ForegroundConfig{Enabled: &enabled, MaxSlices: 48, MaxTotalSeconds: 3600, MaxRetries: &retries}
}

func normalizeForegroundConfig(c *ForegroundConfig) {
	d := DefaultForegroundConfig()
	if c.Enabled == nil {
		c.Enabled = d.Enabled
	}
	if c.MaxRetries == nil || *c.MaxRetries < 0 {
		c.MaxRetries = d.MaxRetries
	}
	if c.MaxSlices <= 0 {
		c.MaxSlices = d.MaxSlices
	}
	if c.MaxTotalSeconds <= 0 {
		c.MaxTotalSeconds = d.MaxTotalSeconds
	}
}
