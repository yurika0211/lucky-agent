package rag

import embedderpkg "github.com/yurika0211/luckyagent/internal/embedder"

func newMockEmbedder(dim int) *embedderpkg.MockEmbedder {
	return embedderpkg.NewMockEmbedder(dim)
}

func newOpenAIEmbedder(cfg embedderpkg.OpenAIEmbedderConfig) *embedderpkg.OpenAIEmbedder {
	return embedderpkg.NewOpenAIEmbedder(cfg)
}
