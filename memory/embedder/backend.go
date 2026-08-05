package embedder

import (
	"context"
)

// OllamaAdapter wraps any Backend that talks to an OpenAI-compatible
// /embeddings endpoint (Ollama in the default setup) behind brain's Embedder
// interface. The Backend interface itself is declared in embedder.go.
type OllamaAdapter struct {
	inner Backend
}

// NewOllamaAdapter binds the adapter to a Backend. For standalone brain use
// NewOllamaHTTP; in the mybrain monolith pass a services.EmbeddingService
// (it satisfies Backend via duck-typing).
func NewOllamaAdapter(inner Backend) *OllamaAdapter {
	return &OllamaAdapter{inner: inner}
}

func (a *OllamaAdapter) Embed(ctx context.Context, content string) ([]float32, error) {
	if content == "" {
		return nil, nil
	}
	vecs, err := a.inner.Embed(ctx, []string{content})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, ErrEmbed
	}
	return vecs[0], nil
}

func (a *OllamaAdapter) EmbedBatch(ctx context.Context, contents []string) ([][]float32, error) {
	if len(contents) == 0 {
		return nil, nil
	}
	return a.inner.Embed(ctx, contents)
}

func (a *OllamaAdapter) Dim() int { return Dim }
