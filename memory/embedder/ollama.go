package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OllamaHTTP is a standalone Backend that calls an OpenAI-compatible
// /embeddings endpoint (Ollama, vLLM, etc.). It exists so brain can embed
// without depending on the mybrain monolith's services.EmbeddingService.
type OllamaHTTP struct {
	BaseURL string        // e.g. "http://localhost:11434/v1"
	APIKey  string        // optional; many local servers ignore it
	Model   string        // e.g. "nomic-embed-text"
	Timeout time.Duration // default 30s
	client  *http.Client
}

// NewOllamaHTTP constructs a standalone embedding backend.
func NewOllamaHTTP(baseURL, apiKey, model string) *OllamaHTTP {
	return &OllamaHTTP{
		BaseURL: baseURL, APIKey: apiKey, Model: model,
		Timeout: 30 * time.Second,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Embed satisfies the Backend interface.
func (o *OllamaHTTP) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if o.client == nil {
		o.client = &http.Client{Timeout: o.Timeout}
	}

	body, err := json.Marshal(map[string]any{
		"model":      o.Model,
		"input":      texts,
		"dimensions": Dim, // hint; server may ignore
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errBody bytes.Buffer
		_, _ = errBody.ReadFrom(resp.Body)
		return nil, fmt.Errorf("embed: status %d: %s", resp.StatusCode, errBody.String())
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embed decode: %w", err)
	}
	vecs := make([][]float32, 0, len(out.Data))
	for _, d := range out.Data {
		vecs = append(vecs, d.Embedding)
	}
	return vecs, nil
}
