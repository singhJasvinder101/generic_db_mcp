// Package config resolves runtime settings (which database to connect to,
// and the latency/safety knobs around querying it) from environment
// variables, so the same binary works against Postgres, MySQL, or SQLite
// with no code changes.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type DBKind string

const (
	Postgres DBKind = "postgres"
	MySQL    DBKind = "mysql"
	SQLite   DBKind = "sqlite"
)

type DBConfig struct {
	Kind       DBKind
	Connection string
}

type RuntimeConfig struct {
	DefaultRowLimit int
	MaxRowLimit     int
	QueryTimeout    time.Duration
	SchemaCacheTTL  time.Duration
}

// ResolveDB reads DATABASE_URL (and optionally DB_TYPE) to figure out which
// adapter to construct and what connection string to hand it.
func ResolveDB() (DBConfig, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return DBConfig{}, fmt.Errorf(
			"DATABASE_URL is not set. Examples: " +
				"postgres://user:pass@host:5432/db, " +
				"mysql://user:pass@host:3306/db, " +
				"sqlite:///absolute/path/to/file.db",
		)
	}

	if explicit := DBKind(strings.ToLower(os.Getenv("DB_TYPE"))); explicit != "" {
		return DBConfig{Kind: explicit, Connection: stripSQLitePrefix(explicit, url)}, nil
	}

	switch {
	case strings.HasPrefix(url, "postgres://"), strings.HasPrefix(url, "postgresql://"):
		return DBConfig{Kind: Postgres, Connection: url}, nil
	case strings.HasPrefix(url, "mysql://"):
		return DBConfig{Kind: MySQL, Connection: url}, nil
	case strings.HasPrefix(url, "sqlite://"), strings.HasSuffix(url, ".db"), strings.HasSuffix(url, ".sqlite"):
		return DBConfig{Kind: SQLite, Connection: stripSQLitePrefix(SQLite, url)}, nil
	default:
		return DBConfig{}, fmt.Errorf(
			"could not infer database type from DATABASE_URL %q; set DB_TYPE to one of: postgres, mysql, sqlite", url,
		)
	}
}

func stripSQLitePrefix(kind DBKind, url string) string {
	if kind == SQLite {
		return strings.TrimPrefix(url, "sqlite://")
	}
	return url
}

// LoadRuntime reads the latency/safety tuning knobs, all optional.
func LoadRuntime() RuntimeConfig {
	return RuntimeConfig{
		DefaultRowLimit: intEnv("DEFAULT_ROW_LIMIT", 200),
		MaxRowLimit:     intEnv("MAX_ROW_LIMIT", 1000),
		QueryTimeout:    time.Duration(intEnv("QUERY_TIMEOUT_MS", 5000)) * time.Millisecond,
		SchemaCacheTTL:  time.Duration(intEnv("SCHEMA_CACHE_TTL_MS", 5*60_000)) * time.Millisecond,
	}
}

func intEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
