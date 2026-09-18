package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"generic-db-mcp/internal/config"
	"generic-db-mcp/internal/dbadapter"
	"generic-db-mcp/internal/schemacache"
	"generic-db-mcp/internal/sqlguard"
)

type deps struct {
	adapter dbadapter.Adapter
	cfg     config.RuntimeConfig
	cache   *schemacache.Cache
}

// Build wires up a new MCP server backed by the given database adapter.
func Build(adapter dbadapter.Adapter, cfg config.RuntimeConfig) *mcp.Server {
	d := &deps{
		adapter: adapter,
		cfg:     cfg,
		cache:   schemacache.New(cfg.SchemaCacheTTL),
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "generic-db-mcp", Version: "0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "list_tables",
		Description: "List every table in the connected database along with its columns, types, " +
			"and primary/foreign keys. Results are cached briefly for speed; pass refresh=true to " +
			"force a fresh read right after a schema change.",
	}, d.listTables)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "describe_table",
		Description: "Get full column detail (types, nullability, keys, defaults) for one table.",
	}, d.describeTable)

	mcp.AddTool(server, &mcp.Tool{
		Name: "run_query",
		Description: "Run a single SELECT statement against the database and return the rows. " +
			"The query is checked and rejected if it is anything other than a plain read (no " +
			"INSERT/UPDATE/DELETE/DDL, no stacked statements); the connection is also held in a " +
			"read-only transaction (or, for SQLite, a read-only file handle) as a second line of " +
			"defense. Results are capped at a row limit to keep responses fast.",
	}, d.runQuery)

	return server
}

type listTablesIn struct {
	Refresh bool `json:"refresh,omitempty" jsonschema:"bypass the schema cache and re-introspect the database"`
}

type tableOut struct {
	Schema  string             `json:"schema,omitempty"`
	Name    string             `json:"name"`
	Columns []dbadapter.Column `json:"columns"`
}

type listTablesOut struct {
	Tables []tableOut `json:"tables"`
}

func (d *deps) listTables(ctx context.Context, _ *mcp.CallToolRequest, in listTablesIn) (*mcp.CallToolResult, listTablesOut, error) {
	if in.Refresh {
		d.cache.Invalidate()
	}

	tables, ok := d.cache.Get()
	if !ok {
		fetched, err := d.adapter.ListTables(ctx)
		if err != nil {
			return nil, listTablesOut{}, fmt.Errorf("listing tables: %w", err)
		}
		d.cache.Set(fetched)
		tables = fetched
	}

	out := listTablesOut{Tables: make([]tableOut, 0, len(tables))}
	for _, t := range tables {
		out.Tables = append(out.Tables, tableOut{Schema: t.Schema, Name: t.Name, Columns: t.Columns})
	}
	return nil, out, nil
}

type describeTableIn struct {
	Table  string `json:"table" jsonschema:"table name to describe"`
	Schema string `json:"schema,omitempty" jsonschema:"schema name, if the database has multiple schemas"`
}

func (d *deps) describeTable(ctx context.Context, _ *mcp.CallToolRequest, in describeTableIn) (*mcp.CallToolResult, tableOut, error) {
	table, err := d.adapter.DescribeTable(ctx, in.Table, in.Schema)
	if err != nil {
		return nil, tableOut{}, fmt.Errorf("describing table %q: %w", in.Table, err)
	}
	if table == nil {
		return nil, tableOut{}, fmt.Errorf("no table named %q was found", in.Table)
	}
	return nil, tableOut{Schema: table.Schema, Name: table.Name, Columns: table.Columns}, nil
}

type runQueryIn struct {
	SQL      string `json:"sql" jsonschema:"a single SELECT statement"`
	RowLimit int    `json:"rowLimit,omitempty" jsonschema:"max rows to return"`
}

type runQueryOut struct {
	Columns    []string         `json:"columns"`
	Rows       []map[string]any `json:"rows"`
	RowCount   int              `json:"rowCount"`
	Truncated  bool             `json:"truncated"`
	DurationMs int64            `json:"durationMs"`
}

func (d *deps) runQuery(ctx context.Context, _ *mcp.CallToolRequest, in runQueryIn) (*mcp.CallToolResult, runQueryOut, error) {
	if err := sqlguard.AssertReadOnlySelect(in.SQL); err != nil {
		return nil, runQueryOut{}, fmt.Errorf("rejected: %w", err)
	}

	limit := in.RowLimit
	if limit <= 0 {
		limit = d.cfg.DefaultRowLimit
	}
	if limit > d.cfg.MaxRowLimit {
		limit = d.cfg.MaxRowLimit
	}

	queryCtx, cancel := context.WithTimeout(ctx, d.cfg.QueryTimeout)
	defer cancel()

	start := time.Now()
	result, err := d.adapter.RunReadOnlyQuery(queryCtx, in.SQL, limit)
	if err != nil {
		return nil, runQueryOut{}, fmt.Errorf("running query: %w", err)
	}

	return nil, runQueryOut{
		Columns:    result.Columns,
		Rows:       result.Rows,
		RowCount:   result.RowCount,
		Truncated:  result.Truncated,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}
