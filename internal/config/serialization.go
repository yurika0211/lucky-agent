package config

import "encoding/json"

// A legacy vision flag without a legacy model describes the canonical chat
// selection. Preserve that association before defaults or aliases are applied.
func (c *Config) UnmarshalJSON(data []byte) error {
	type plain Config
	if err := json.Unmarshal(data, (*plain)(c)); err != nil {
		return err
	}
	var source struct {
		LLM struct {
			Model  *string `json:"model"`
			Vision bool    `json:"vision"`
		} `json:"llm_provider"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return err
	}
	if source.LLM.Vision && source.LLM.Model == nil && c.Models.Active[ModelKindChat] != "" {
		c.LlmProvider.Model = c.Models.Active[ModelKindChat]
	}
	return nil
}

// MarshalJSON writes only canonical model selections. Legacy fields remain
// readable and serve existing in-process consumers, but are never persisted
// alongside their replacements or exposed as duplicate controls in the API.
func (c Config) MarshalJSON() ([]byte, error) {
	if err := validateModelConfig(&c); err != nil {
		return nil, err
	}
	next := cloneConfig(&c)
	normalizeModels(next)
	type plain Config
	data, err := json.Marshal((*plain)(next))
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	delete(out, "llm_provider")
	delete(out, "extra_headers")
	delete(out, "multimodal")
	for _, section := range []string{"embedding", "image_generation", "tts"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(out[section], &fields); err != nil {
			return nil, err
		}
		for _, key := range []string{"model", "provider", "api_key", "api_base"} {
			delete(fields, key)
		}
		out[section], err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(out)
}
