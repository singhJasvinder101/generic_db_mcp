package vectorstore

import (
	"fmt"

	"generic-db-mcp/internal/config"
)

func New(cfg config.VectorStoreConfig) (Store, error) {
	switch cfg.Kind {
	case "", "memory":
		return NewMemory(cfg.Path)
	default:
		return nil, fmt.Errorf("unsupported vector store kind: %q", cfg.Kind)
	}
}
