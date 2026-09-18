// Command generic-db-mcp is a Model Context Protocol server that lets any
// MCP-compatible agent explore the schema of, and run read-only SQL
// against, a Postgres, MySQL, or SQLite database over stdio.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"generic-db-mcp/internal/config"
	"generic-db-mcp/internal/dbadapter"
	"generic-db-mcp/internal/mcpserver"
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

	server := mcpserver.Build(adapter, runtimeCfg)

	log.Printf("MCP server ready on stdio")
	return server.Run(ctx, &mcp.StdioTransport{})
}
