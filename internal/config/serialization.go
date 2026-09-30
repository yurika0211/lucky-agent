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
	stripPersistedCredentialValues(out)
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

// stripPersistedCredentialValues keeps credential references in the config
// while ensuring a legacy API key cannot be written beside a reference. The
// key remains available in the in-memory normalized snapshot for runtime use.
func stripPersistedCredentialValues(out map[string]json.RawMessage) {
	stripPersistedFallbackValues(out)
	rawModels, ok := out["models"]
	if !ok {
		return
	}
	var models map[string]json.RawMessage
	if json.Unmarshal(rawModels, &models) != nil {
		return
	}
	rawEndpoints, ok := models["endpoints"]
	if !ok {
		return
	}
	var endpoints map[string]json.RawMessage
	if json.Unmarshal(rawEndpoints, &endpoints) != nil {
		return
	}
	for kind, rawEndpoint := range endpoints {
		var endpoint map[string]json.RawMessage
		if json.Unmarshal(rawEndpoint, &endpoint) != nil {
			continue
		}
		ref, ok := endpoint["credential_ref"]
		if !ok {
			continue
		}
		var reference string
		if json.Unmarshal(ref, &reference) == nil && reference != "" {
			delete(endpoint, "api_key")
			if encoded, err := json.Marshal(endpoint); err == nil {
				endpoints[kind] = encoded
			}
		}
	}
	if encoded, err := json.Marshal(endpoints); err == nil {
		models["endpoints"] = encoded
		if encoded, err := json.Marshal(models); err == nil {
			out["models"] = encoded
		}
	}
}

func stripPersistedFallbackValues(out map[string]json.RawMessage) {
	rawFallbacks, ok := out["fallbacks"]
	if !ok {
		return
	}
	var fallbacks []json.RawMessage
	if json.Unmarshal(rawFallbacks, &fallbacks) != nil {
		return
	}
	for index, rawFallback := range fallbacks {
		var fallback map[string]json.RawMessage
		if json.Unmarshal(rawFallback, &fallback) != nil {
			continue
		}
		ref, ok := fallback["credential_ref"]
		if !ok {
			continue
		}
		var reference string
		if json.Unmarshal(ref, &reference) == nil && reference != "" {
			delete(fallback, "api_key")
			if encoded, err := json.Marshal(fallback); err == nil {
				fallbacks[index] = encoded
			}
		}
	}
	if encoded, err := json.Marshal(fallbacks); err == nil {
		out["fallbacks"] = encoded
	}
}
