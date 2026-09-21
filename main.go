// Command generic-db-mcp is a Model Context Protocol server that lets any
// MCP-compatible agent explore the schema of, and run read-only SQL
// against, a Postgres, MySQL, or SQLite database.
//
// By default it speaks MCP over stdio, the right mode for a single desktop
// agent (Claude Desktop/Code, Cursor, ...) that spawns its own copy of this
// process. Set MCP_TRANSPORT=http to instead run one long-lived server that
// many concurrent MCP clients can share over the network — useful when a
// lot of a client's own agents/users need to hit the same database, since
// they then share this one process's connection pool instead of each
// spawning their own.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"generic-db-mcp/internal/config"
	"generic-db-mcp/internal/dbadapter"
	"generic-db-mcp/internal/embeddings"
	"generic-db-mcp/internal/httpserver"
	"generic-db-mcp/internal/mcpserver"
	"generic-db-mcp/internal/vectorstore"
)

func main() {
	_ = godotenv.Load() // optional .env; real env vars always win

	if err := run(); err != nil {
		log.Fatalf("generic-db-mcp: %v", err)
	}
}

func run() error {
	dbCfg, err := config.ResolveDB()
	if err != nil {
		return err
	}
	runtimeCfg := config.LoadRuntime()

	adapter, err := dbadapter.New(dbCfg)
	if err != nil {
		return fmt.Errorf("creating %s adapter: %w", dbCfg.Kind, err)
	}
	defer adapter.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := adapter.Connect(ctx); err != nil {
		return fmt.Errorf("connecting to %s database: %w", dbCfg.Kind, err)
	}
	log.Printf("connected to %s database", dbCfg.Kind)

	var embedder embeddings.Provider
	var vectors vectorstore.Store
	if config.SchemaContextEnabled() {
		embedder, err = embeddings.New(config.LoadEmbedding())
		if err != nil {
			return fmt.Errorf("creating embedding provider: %w", err)
		}
		vectors, err = vectorstore.New(config.LoadVectorStore())
		if err != nil {
			return fmt.Errorf("creating vector store: %w", err)
		}
	}

	server := mcpserver.Build(adapter, runtimeCfg, embedder, vectors)

	switch transport := strings.ToLower(os.Getenv("MCP_TRANSPORT")); transport {
	case "", "stdio":
		log.Printf("MCP server ready on stdio")
		return server.Run(ctx, &mcp.StdioTransport{})
	case "http":
		addr := os.Getenv("MCP_HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		return httpserver.Run(ctx, addr, os.Getenv("MCP_HTTP_AUTH_TOKEN"), server)
	default:
		return fmt.Errorf(`unknown MCP_TRANSPORT %q (want "stdio" or "http")`, transport)
	}
}
