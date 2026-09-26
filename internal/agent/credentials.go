package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/credentials"
)

// resolveConfiguredCredentials resolves credential_ref values into the local
// runtime snapshot used to construct providers. The config manager itself is
// never mutated, so encrypted references remain the only persisted form.
func resolveConfiguredCredentials(homeDir string, c *config.Config) error {
	if c == nil {
		return nil
	}
	refs := false
	for _, endpoint := range c.Models.Endpoints {
		if strings.TrimSpace(endpoint.CredentialRef) != "" {
			refs = true
			break
		}
	}
	if !refs {
		for _, fallback := range c.Fallbacks {
			if strings.TrimSpace(fallback.CredentialRef) != "" {
				refs = true
				break
			}
		}
	}
	if !refs {
		return nil
	}

	store, err := credentials.NewStore(homeDir)
	if err != nil {
		return fmt.Errorf("open credential store: %w", err)
	}
	defer store.Close()

	for kind, endpoint := range c.Models.Endpoints {
		ref := strings.TrimSpace(endpoint.CredentialRef)
		if ref == "" {
			continue
		}
		kind, endpoint, ref := kind, endpoint, ref
		err := store.WithCredential(context.Background(), ref, func(secret []byte) error {
			endpoint.APIKey = string(secret)
			c.Models.Endpoints[kind] = endpoint
			applyResolvedModelCredential(c, kind, endpoint.APIKey)
			return nil
		})
		if err != nil {
			return fmt.Errorf("resolve credential %q for model kind %q: %w", ref, kind, err)
		}
	}
	for index := range c.Fallbacks {
		ref := strings.TrimSpace(c.Fallbacks[index].CredentialRef)
		if ref == "" {
			continue
		}
		index, ref := index, ref
		kind := strings.TrimSpace(c.Fallbacks[index].Provider)
		if kind == "" {
			kind = "fallback"
		}
		if err := store.WithCredential(context.Background(), ref, func(secret []byte) error {
			c.Fallbacks[index].APIKey = string(secret)
			return nil
		}); err != nil {
			return fmt.Errorf("resolve credential %q for fallback %q: %w", ref, kind, err)
		}
	}
	return nil
}

func applyResolvedModelCredential(c *config.Config, kind config.ModelKind, value string) {
	switch kind {
	case config.ModelKindChat:
		c.LlmProvider.APIKey = value
		c.APIKey = value
	case config.ModelKindEmbedding:
		c.Embedding.APIKey = value
	case config.ModelKindVision, config.ModelKindTranscription:
		c.Multimodal.APIKey = value
	case config.ModelKindImage:
		c.ImageGeneration.APIKey = value
	case config.ModelKindTTS:
		c.TTS.APIKey = value
	}
}
