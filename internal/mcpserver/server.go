package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"generic-db-mcp/internal/config"
	"generic-db-mcp/internal/dbadapter"
	"generic-db-mcp/internal/embeddings"
	"generic-db-mcp/internal/schemacache"
	"generic-db-mcp/internal/sqlguard"
	"generic-db-mcp/internal/vectorstore"
)

type deps struct {
	adapter  dbadapter.Adapter
	cfg      config.RuntimeConfig
	cache    *schemacache.Cache
	embedder embeddings.Provider
	vectors  vectorstore.Store
}
func Build(adapter dbadapter.Adapter, cfg config.RuntimeConfig, embedder embeddings.Provider, vectors vectorstore.Store) *mcp.Server {
	d := &deps{
		adapter:  adapter,
		cfg:      cfg,
		cache:    schemacache.New(cfg.SchemaCacheTTL),
		embedder: embedder,
		vectors:  vectors,
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

	if d.embedder != nil && d.vectors != nil {
		mcp.AddTool(server, &mcp.Tool{
			Name: "teach_schema_context",
			Description: "Record what ONE table or column actually means in plain language — call this " +
				"whenever a name alone is ambiguous or misleading (e.g. a table named 'tbl2' that " +
				"actually holds orders, or a column whose values need business context to interpret). " +
				"Re-calling with the same table+column replaces the previous note. If you're recording " +
				"context for more than one table/column in the same turn (e.g. a user just explained " +
				"their whole schema at once), use teach_schema_context_bulk instead of calling this " +
				"tool repeatedly — it embeds and stores everything in one batch.",
		}, d.teachSchemaContext)

		mcp.AddTool(server, &mcp.Tool{
			Name: "teach_schema_context_bulk",
			Description: "Same as teach_schema_context, but for many tables/columns at once — one " +
				"embedding batch and one storage write instead of one tool call per table. Use this " +
				"whenever a user describes several tables/columns in a single message (e.g. walking " +
				"through their whole schema) instead of calling teach_schema_context once per table.",
		}, d.teachSchemaContextBulk)

		mcp.AddTool(server, &mcp.Tool{
			Name: "search_schema_context",
			Description: "Semantic search over notes previously saved with teach_schema_context. Call " +
				"this before writing a query against an unfamiliar table/column, or whenever the " +
				"schema's naming doesn't make the intent obvious, to check whether a human has already " +
				"explained what it actually means.",
		}, d.searchSchemaContext)
	}

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

type teachSchemaContextIn struct {
	Schema      string   `json:"schema,omitempty" jsonschema:"schema name, if the database has multiple schemas"`
	Table       string   `json:"table" jsonschema:"table this note is about"`
	Column      string   `json:"column,omitempty" jsonschema:"column this note is about, if it's specific to one column rather than the whole table"`
	Description string   `json:"description" jsonschema:"what this table/column actually represents in plain language, including anything its name doesn't make obvious"`
	Aliases     []string `json:"aliases,omitempty" jsonschema:"other names/terms a user might use to refer to this table or column"`
}

type teachSchemaContextOut struct {
	ID string `json:"id"`
}

func (d *deps) teachSchemaContext(ctx context.Context, _ *mcp.CallToolRequest, in teachSchemaContextIn) (*mcp.CallToolResult, teachSchemaContextOut, error) {
	ids, err := d.teachEntries(ctx, []teachSchemaContextIn{in})
	if err != nil {
		return nil, teachSchemaContextOut{}, err
	}
	return nil, teachSchemaContextOut{ID: ids[0]}, nil
}

type teachSchemaContextBulkIn struct {
	Entries []teachSchemaContextIn `json:"entries" jsonschema:"one entry per table/column to record context for — seed everything in one call instead of one tool call per table"`
}

type teachSchemaContextBulkOut struct {
	IDs []string `json:"ids"`
}

func (d *deps) teachSchemaContextBulk(ctx context.Context, _ *mcp.CallToolRequest, in teachSchemaContextBulkIn) (*mcp.CallToolResult, teachSchemaContextBulkOut, error) {
	ids, err := d.teachEntries(ctx, in.Entries)
	if err != nil {
		return nil, teachSchemaContextBulkOut{}, err
	}
	return nil, teachSchemaContextBulkOut{IDs: ids}, nil
}

// teachEntries embeds and stores every entry in a single batch: one call to
// the embedding provider and one write to the vector store, regardless of
// how many entries there are.
func (d *deps) teachEntries(ctx context.Context, entries []teachSchemaContextIn) ([]string, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("at least one entry is required")
	}

	ids := make([]string, len(entries))
	texts := make([]string, len(entries))
	for i, e := range entries {
		if e.Table == "" {
			return nil, fmt.Errorf("entries[%d]: table is required", i)
		}
		if e.Description == "" {
			return nil, fmt.Errorf("entries[%d]: description is required", i)
		}
		id := e.Schema + "." + e.Table
		if e.Column != "" {
			id += "." + e.Column
		}
		ids[i] = id
		texts[i] = buildSchemaContextText(e)
	}

	vecs, err := d.embedder.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embedding context: %w", err)
	}

	docs := make([]vectorstore.Document, len(entries))
	for i, e := range entries {
		docs[i] = vectorstore.Document{
			ID:        ids[i],
			Schema:    e.Schema,
			Table:     e.Table,
			Column:    e.Column,
			Text:      texts[i],
			Embedding: vecs[i],
		}
	}
	if err := d.vectors.Upsert(ctx, docs); err != nil {
		return nil, fmt.Errorf("storing context: %w", err)
	}

	return ids, nil
}

func buildSchemaContextText(in teachSchemaContextIn) string {
	var b strings.Builder
	fmt.Fprintf(&b, "table: %s\n", in.Table)
	if in.Column != "" {
		fmt.Fprintf(&b, "column: %s\n", in.Column)
	}
	if len(in.Aliases) > 0 {
		fmt.Fprintf(&b, "also known as: %s\n", strings.Join(in.Aliases, ", "))
	}
	fmt.Fprintf(&b, "meaning: %s", in.Description)
	return b.String()
}

type searchSchemaContextIn struct {
	Query string `json:"query" jsonschema:"natural-language question or topic to find relevant table/column context for"`
	TopK  int    `json:"topK,omitempty" jsonschema:"max number of matches to return (default 5)"`
}

type schemaContextMatch struct {
	Schema      string  `json:"schema,omitempty"`
	Table       string  `json:"table"`
	Column      string  `json:"column,omitempty"`
	Description string  `json:"description"`
	Score       float32 `json:"score"`
}

type searchSchemaContextOut struct {
	Matches []schemaContextMatch `json:"matches"`
}

func (d *deps) searchSchemaContext(ctx context.Context, _ *mcp.CallToolRequest, in searchSchemaContextIn) (*mcp.CallToolResult, searchSchemaContextOut, error) {
	if in.Query == "" {
		return nil, searchSchemaContextOut{}, fmt.Errorf("query is required")
	}

	topK := in.TopK
	if topK <= 0 {
		topK = 5
	}

	vecs, err := d.embedder.Embed(ctx, []string{in.Query})
	if err != nil {
		return nil, searchSchemaContextOut{}, fmt.Errorf("embedding query: %w", err)
	}

	matches, err := d.vectors.Query(ctx, vecs[0], topK)
	if err != nil {
		return nil, searchSchemaContextOut{}, fmt.Errorf("searching context: %w", err)
	}

	out := searchSchemaContextOut{Matches: make([]schemaContextMatch, 0, len(matches))}
	for _, m := range matches {
		out.Matches = append(out.Matches, schemaContextMatch{
			Schema:      m.Schema,
			Table:       m.Table,
			Column:      m.Column,
			Description: m.Text,
			Score:       m.Score,
		})
	}
	return nil, out, nil
}
