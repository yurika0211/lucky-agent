package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultModelDiscoveryCacheTTL = 30 * time.Second

var ErrModelDiscoveryUnsupported = errors.New("model discovery is not supported by this provider")

type ModelDiscoveryConfig struct {
	Provider     string
	APIKey       string
	APIBase      string
	Model        string
	ExtraHeaders map[string]string
}

type ModelDiscoveryErrorKind string

const (
	ModelDiscoveryAuthentication ModelDiscoveryErrorKind = "authentication"
	ModelDiscoveryRateLimited    ModelDiscoveryErrorKind = "rate_limited"
	ModelDiscoveryUnsupported    ModelDiscoveryErrorKind = "unsupported"
	ModelDiscoveryNetwork        ModelDiscoveryErrorKind = "network"
	ModelDiscoveryResponse       ModelDiscoveryErrorKind = "response"
)

type ModelDiscoveryError struct {
	Kind   ModelDiscoveryErrorKind
	Status int
	Err    error
}

func (e *ModelDiscoveryError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return string(e.Kind)
	}
	return fmt.Sprintf("%s: %v", e.Kind, e.Err)
}

func (e *ModelDiscoveryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type cachedModelList struct {
	models  []ModelInfo
	expires time.Time
}

type ModelDiscovery struct {
	mu       sync.Mutex
	cache    map[string]cachedModelList
	client   *http.Client
	cacheTTL time.Duration
}

func NewModelDiscovery() *ModelDiscovery {
	return &ModelDiscovery{
		cache:    make(map[string]cachedModelList),
		client:   &http.Client{Timeout: 12 * time.Second},
		cacheTTL: defaultModelDiscoveryCacheTTL,
	}
}

func (d *ModelDiscovery) Discover(ctx context.Context, cfg ModelDiscoveryConfig, refresh bool) ([]ModelInfo, error) {
	if d == nil {
		d = NewModelDiscovery()
	}
	key := modelDiscoveryCacheKey(cfg)
	if !refresh {
		d.mu.Lock()
		entry, ok := d.cache[key]
		d.mu.Unlock()
		if ok && time.Now().Before(entry.expires) {
			return cloneDiscoveredModels(entry.models), nil
		}
	}

	models, err := discoverModels(ctx, d.httpClient(), cfg)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.cache[key] = cachedModelList{models: cloneDiscoveredModels(models), expires: time.Now().Add(d.cacheTTL)}
	d.mu.Unlock()
	return models, nil
}

func (d *ModelDiscovery) httpClient() *http.Client {
	if d != nil && d.client != nil {
		return d.client
	}
	return &http.Client{Timeout: 12 * time.Second}
}

func modelDiscoveryCacheKey(cfg ModelDiscoveryConfig) string {
	credentialHash := sha256.Sum256([]byte(strings.TrimSpace(cfg.APIKey)))
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(cfg.Provider)),
		strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/"),
		strings.TrimSpace(cfg.Model),
		hex.EncodeToString(credentialHash[:]),
	}, "\x00")
}

func discoverModels(ctx context.Context, client *http.Client, cfg ModelDiscoveryConfig) ([]ModelInfo, error) {
	providerName := strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch providerName {
	case "openai", "openai-compatible", "openrouter", "anthropic":
		return discoverOpenAIStyleModels(ctx, client, cfg)
	case "ollama":
		return discoverOllamaModels(ctx, client, cfg)
	default:
		return nil, fmt.Errorf("%w: %s", ErrModelDiscoveryUnsupported, providerName)
	}
}

func discoverOpenAIStyleModels(ctx context.Context, client *http.Client, cfg ModelDiscoveryConfig) ([]ModelInfo, error) {
	endpoint, err := modelEndpoint(cfg.APIBase, cfg.Provider)
	if err != nil {
		return nil, &ModelDiscoveryError{Kind: ModelDiscoveryUnsupported, Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &ModelDiscoveryError{Kind: ModelDiscoveryResponse, Err: errors.New("could not prepare model-list request")}
	}
	applyDiscoveryHeaders(req, cfg)

	resp, err := client.Do(req)
	if err != nil {
		return nil, &ModelDiscoveryError{Kind: ModelDiscoveryNetwork, Err: errors.New("model endpoint could not be reached")}
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, newModelDiscoveryHTTPError(resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, &ModelDiscoveryError{Kind: ModelDiscoveryResponse, Err: errors.New("model endpoint returned an invalid response")}
	}
	models := make([]ModelInfo, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		displayName := firstNonEmpty(item.DisplayName, item.Name, id)
		models = append(models, ModelInfo{ID: id, Provider: strings.TrimSpace(cfg.Provider), DisplayName: displayName})
	}
	return normalizeDiscoveredModels(models)
}

func discoverOllamaModels(ctx context.Context, client *http.Client, cfg ModelDiscoveryConfig) ([]ModelInfo, error) {
	endpoint, err := ollamaModelsEndpoint(cfg.APIBase)
	if err != nil {
		return nil, &ModelDiscoveryError{Kind: ModelDiscoveryUnsupported, Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &ModelDiscoveryError{Kind: ModelDiscoveryResponse, Err: errors.New("could not prepare model-list request")}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &ModelDiscoveryError{Kind: ModelDiscoveryNetwork, Err: errors.New("model endpoint could not be reached")}
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, newModelDiscoveryHTTPError(resp.StatusCode)
	}
	var payload struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return nil, &ModelDiscoveryError{Kind: ModelDiscoveryResponse, Err: errors.New("model endpoint returned an invalid response")}
	}
	models := make([]ModelInfo, 0, len(payload.Models))
	for _, item := range payload.Models {
		id := firstNonEmpty(item.Model, item.Name)
		if id == "" {
			continue
		}
		models = append(models, ModelInfo{ID: id, Provider: "ollama", DisplayName: firstNonEmpty(item.Name, id)})
	}
	return normalizeDiscoveredModels(models)
}

func applyDiscoveryHeaders(req *http.Request, cfg ModelDiscoveryConfig) {
	for key, value := range cfg.ExtraHeaders {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	if apiKey := strings.TrimSpace(cfg.APIKey); apiKey != "" {
		if strings.EqualFold(strings.TrimSpace(cfg.Provider), "anthropic") {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
}

func newModelDiscoveryHTTPError(status int) error {
	kind := ModelDiscoveryResponse
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		kind = ModelDiscoveryAuthentication
	case http.StatusTooManyRequests:
		kind = ModelDiscoveryRateLimited
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		kind = ModelDiscoveryUnsupported
	}
	return &ModelDiscoveryError{Kind: kind, Status: status, Err: fmt.Errorf("model endpoint returned HTTP %d", status)}
}

func modelEndpoint(apiBase, providerName string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		switch strings.ToLower(strings.TrimSpace(providerName)) {
		case "openai", "openai-compatible":
			base = "https://api.openai.com/v1"
		case "openrouter":
			base = "https://openrouter.ai/api/v1"
		case "anthropic":
			base = "https://api.anthropic.com/v1"
		default:
			return "", errors.New("provider has no configured model-list endpoint")
		}
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("configured API base is invalid")
	}
	u.RawQuery = ""
	u.Fragment = ""
	path := strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/messages"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
			break
		}
	}
	u.Path = strings.TrimRight(path, "/") + "/models"
	return u.String(), nil
}

func ollamaModelsEndpoint(apiBase string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		base = "http://localhost:11434"
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("configured API base is invalid")
	}
	u.RawQuery = ""
	u.Fragment = ""
	path := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(path, "/api/tags") {
		u.Path = path
	} else if strings.HasSuffix(path, "/api") {
		u.Path = path + "/tags"
	} else {
		u.Path = path + "/api/tags"
	}
	return u.String(), nil
}

func normalizeDiscoveredModels(models []ModelInfo) ([]ModelInfo, error) {
	unique := make(map[string]ModelInfo, len(models))
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			continue
		}
		if strings.TrimSpace(model.DisplayName) == "" {
			model.DisplayName = model.ID
		}
		unique[model.ID] = model
	}
	result := make([]ModelInfo, 0, len(unique))
	for _, model := range unique {
		result = append(result, model)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if len(result) == 0 {
		return nil, &ModelDiscoveryError{Kind: ModelDiscoveryResponse, Err: errors.New("model endpoint returned no model IDs")}
	}
	return result, nil
}

func cloneDiscoveredModels(models []ModelInfo) []ModelInfo {
	result := make([]ModelInfo, len(models))
	copy(result, models)
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
