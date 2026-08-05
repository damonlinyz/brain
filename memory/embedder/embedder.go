// Package embedder wraps the project-wide EmbeddingService behind the V2
// memory Embedder interface, plus an optional Redis cache layer.
package embedder

import (
	"context"
	"errors"
)

// Dim is the project-wide embedding dimension (nomic-embed-text = 768).
const Dim = 768

// Embedder is the V2 memory hub embedding interface. Implementations must be
// safe for concurrent use.
type Embedder interface {
	Embed(ctx context.Context, content string) ([]float32, error)
	EmbedBatch(ctx context.Context, contents []string) ([][]float32, error)
	Dim() int
}

// ErrEmbed is returned when the underlying embedding call fails.
var ErrEmbed = errors.New("embed: backend error")

// Backend is the minimal surface from services.EmbeddingService we depend on.
// Defined here so the V2 memory package does not import services directly
// (avoid cycles when services eventually imports memory).
type Backend interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
