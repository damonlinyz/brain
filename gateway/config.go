package gateway

import (
	"os"
)

// Config is brain's runtime configuration, all env-driven.
type Config struct {
	DatabaseURL string

	// Embedder (for memory vector recall/ingest).
	EmbeddingBaseURL string
	EmbeddingAPIKey  string
	EmbeddingModel   string

	// Upstream LLM that the gateway forwards chat to (the model the CLI/user
	// chose). Brain itself is model-agnostic — it just injects memory + forwards.
	UpstreamLLMBaseURL string
	UpstreamLLMAPIKey  string
	UpstreamLLMModel   string

	// Server.
	Port string

	// Local single-user mode: the owner whose memories the gateway reads/writes.
	// (Multi-user auth is the monolith's job; standalone brain is for one user.)
	UserID string

	// Bearer token clients must present (simple gate; set to disable auth).
	Token string
}

// LoadConfig reads configuration from the environment with sane defaults.
func LoadConfig() Config {
	return Config{
		DatabaseURL:        env("DATABASE_URL", "postgres://localhost:5432/mybrain?sslmode=disable"),
		EmbeddingBaseURL:   env("EMBEDDING_BASE_URL", "http://localhost:11434/v1"),
		EmbeddingAPIKey:    os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingModel:     env("EMBEDDING_MODEL", "nomic-embed-text"),
		UpstreamLLMBaseURL: env("UPSTREAM_LLM_BASE_URL", "https://api.deepseek.com/v1"),
		UpstreamLLMAPIKey:  os.Getenv("UPSTREAM_LLM_API_KEY"),
		UpstreamLLMModel:   env("UPSTREAM_LLM_MODEL", "deepseek-chat"),
		Port:               env("BRAIN_PORT", "8090"),
		UserID:             os.Getenv("BRAIN_USER_ID"),
		Token:              os.Getenv("BRAIN_TOKEN"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
