package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func canonicalModelKey(key string) string {
	aliases := map[string]string{
		"provider": "models.endpoints.chat.provider", "api_key": "models.endpoints.chat.api_key",
		"api_base": "models.endpoints.chat.api_base", "model": "models.active.chat",
		"protocol": "models.endpoints.chat.protocol", "llm_provider.name": "models.endpoints.chat.provider",
		"llm_provider.api_key": "models.endpoints.chat.api_key", "llm_provider.base_url": "models.endpoints.chat.api_base",
		"llm_provider.model": "models.active.chat", "llm_provider.protocol": "models.endpoints.chat.protocol",
		"llm_provider.reasoning_summary": "models.endpoints.chat.reasoning_summary",
		"multimodal.image_model":         "models.active.vision", "multimodal.transcription_model": "models.active.transcription",
	}
	if alias, ok := aliases[key]; ok {
		return alias
	}
	for section, kind := range map[string]string{"embedding": "embedding", "image_generation": "image", "tts": "tts"} {
		if key == section+".model" {
			return "models.active." + kind
		}
		for _, field := range []string{"provider", "api_key", "api_base"} {
			if key == section+"."+field {
				return "models.endpoints." + kind + "." + field
			}
		}
	}
	if strings.HasPrefix(key, "extra_headers.") {
		return "models.endpoints.chat." + key
	}
	return key
}

func (c *Config) setModelKey(key, value string) (bool, error) {
	if key == "multimodal.image_provider" {
		switch value {
		case "", "openai-media":
			value = "openai"
		case "local":
		default:
			// Preserve names supplied by integrations registering custom processors.
		}
		key = "models.endpoints.vision.provider"
	}
	for _, field := range []string{"provider", "api_key", "api_base"} {
		if key == "multimodal."+field {
			for _, kind := range []string{"vision", "transcription"} {
				if _, err := c.setModelKey("models.endpoints."+kind+"."+field, value); err != nil {
					return true, err
				}
			}
			return true, nil
		}
	}
	key = canonicalModelKey(key)
	if !strings.HasPrefix(key, "models.") {
		return false, nil
	}
	if key == "models.vision_mode" {
		if value != "auto" && value != "external" {
			return true, fmt.Errorf("models.vision_mode must be auto or external")
		}
		c.Models.VisionMode = value
		return true, nil
	}
	parts := strings.SplitN(key, ".", 4)
	if len(parts) < 3 {
		return true, fmt.Errorf("invalid model configuration key %q", key)
	}
	kind, err := ParseModelKind(parts[2])
	if err != nil {
		return true, err
	}
	normalizeModels(c)
	if parts[1] == "active" && len(parts) == 3 {
		c.Models.Active[kind] = strings.TrimSpace(value)
	} else if parts[1] == "endpoints" && len(parts) == 4 {
		ep := c.ModelEndpoint(kind)
		switch parts[3] {
		case "provider":
			ep.Provider = value
		case "api_key":
			ep.APIKey = value
		case "api_base":
			ep.APIBase = value
		case "protocol":
			ep.Protocol = value
		case "reasoning_summary":
			ep.ReasoningSummary = value
		case "timeout_seconds":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return true, fmt.Errorf("invalid endpoint timeout %q", value)
			}
			ep.TimeoutSeconds = n
		default:
			if !strings.HasPrefix(parts[3], "extra_headers.") {
				return true, fmt.Errorf("unknown endpoint field %q", parts[3])
			}
			if ep.ExtraHeaders == nil {
				ep.ExtraHeaders = map[string]string{}
			}
			ep.ExtraHeaders[strings.TrimPrefix(parts[3], "extra_headers.")] = value
		}
		c.Models.Endpoints[kind] = ep
	} else {
		return true, fmt.Errorf("invalid model configuration key %q", key)
	}
	syncLegacyModels(c)
	return true, nil
}

// ModelConfigValue serves canonical CLI keys (and unambiguous legacy aliases).
func (c *Config) ModelConfigValue(key string) (string, bool, error) {
	key = canonicalModelKey(key)
	if !strings.HasPrefix(key, "models.") {
		return "", false, nil
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", true, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return "", true, err
	}
	for _, part := range strings.Split(key, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return "", true, fmt.Errorf("unknown config key %q", key)
		}
		value = object[part]
	}
	if value == nil {
		return "", true, nil
	}
	if s, ok := value.(string); ok {
		return s, true, nil
	}
	data, err = json.Marshal(value)
	return string(data), true, err
}
