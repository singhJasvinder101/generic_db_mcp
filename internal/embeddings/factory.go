package embeddings

import (
	"fmt"

	"generic-db-mcp/internal/config"
)

func New(cfg config.EmbeddingConfig) (Provider, error) {
	switch cfg.Provider {
	case "", "ollama":
		return NewOllama(cfg.BaseURL, cfg.Model), nil
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %q", cfg.Provider)
	}
}
