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

// EmbeddingConfig picks how taught schema context and search queries get
// turned into vectors. "ollama" (the only provider today) calls a local
// Ollama server, so this never leaves the client's machine or costs a
// per-call API fee.
type EmbeddingConfig struct {
	Provider string
	BaseURL  string
	Model    string
}

// VectorStoreConfig picks where taught schema context is stored. "memory"
// (the only kind today) keeps everything in memory and persists it to a
// JSON file at Path.
type VectorStoreConfig struct {
	Kind string
	Path string
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

// SchemaContextEnabled reports whether teach_schema_context/
// search_schema_context should be registered. Off by default: they need a
// local Ollama server, which not every environment has running, so a client
// opts in explicitly rather than the two tools silently failing every call.
func SchemaContextEnabled() bool {
	return boolEnv("SCHEMA_CONTEXT_ENABLED", false)
}

func boolEnv(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

// LoadEmbedding reads the embedding-provider settings, all optional.
func LoadEmbedding() EmbeddingConfig {
	return EmbeddingConfig{
		Provider: strEnv("EMBEDDING_PROVIDER", "ollama"),
		BaseURL:  strEnv("OLLAMA_BASE_URL", "http://localhost:11434"),
		Model:    strEnv("EMBEDDING_MODEL", "bge-m3"),
	}
}

// LoadVectorStore reads the vector-store settings, all optional.
func LoadVectorStore() VectorStoreConfig {
	return VectorStoreConfig{
		Kind: strEnv("VECTOR_STORE_KIND", "memory"),
		Path: strEnv("VECTOR_STORE_PATH", "schema_context.json"),
	}
}

func strEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
