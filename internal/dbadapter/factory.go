package dbadapter

import (
	"fmt"

	"generic-db-mcp/internal/config"
)

func New(cfg config.DBConfig) (Adapter, error) {
	switch cfg.Kind {
	case config.Postgres:
		return NewPostgres(cfg.Connection)
	case config.MySQL:
		return NewMySQL(cfg.Connection)
	case config.SQLite:
		return NewSQLite(cfg.Connection)
	default:
		return nil, fmt.Errorf("unsupported database kind: %q", cfg.Kind)
	}
}
