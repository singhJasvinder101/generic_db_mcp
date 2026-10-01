# generic-db-mcp

A single [Model Context Protocol](https://modelcontextprotocol.io) server
that lets any MCP-compatible agent (Claude Code, Claude Desktop, Cursor,
etc.) explore the schema of, and run **read-only** SQL against, a
**Postgres, MySQL, or SQLite** database — over stdio, with no code changes
between engines.

## Why

- **Generic**: one binary, driven entirely by `DATABASE_URL`. Point it at a
  different engine and it adapts.
- **Read-only by construction**: every query is checked (a lexical guard
  rejects anything but a single `SELECT`/`WITH` statement) *and* run inside a
  real read-only transaction (`BEGIN TRANSACTION READ ONLY` on Postgres,
  `START TRANSACTION READ ONLY` on MySQL, a `mode=ro` file handle on SQLite).
  Either layer alone would stop a mutation; both are on by default.
- **Low latency**: pooled connections, a short-lived in-memory schema cache
  (so repeated "what tables exist" calls don't re-walk
  `information_schema`), a per-query timeout, and a hard row cap so a huge
  table can't stall a response.

## Tools exposed to the agent

| Tool | Purpose |
|---|---|
| `list_tables` | Every table with its columns, types, and primary/foreign keys. Cached; pass `refresh: true` after a schema change. |
| `describe_table` | Full column detail for one table. |
| `run_query` | Run one `SELECT` and get back rows, capped at `rowLimit` (default/max configurable). |
| `teach_schema_context`* | Record what one table/column actually means in plain language — for when names are ambiguous or misleading. |
| `teach_schema_context_bulk`* | Same, for many tables/columns in one call (one embedding batch, one write) — use this when a user explains several tables at once. |
| `search_schema_context`* | Semantic search over notes saved with `teach_schema_context`(`_bulk`), so an agent can check for human-provided context before querying an unfamiliar table. |

\* Only registered when `SCHEMA_CONTEXT_ENABLED=true` — see "Schema business context" below.

## Setup

```bash
go build -o bin/generic-db-mcp .
cp .env.example .env   # then edit DATABASE_URL
```

`DATABASE_URL` examples:

```
postgres://user:pass@host:5432/dbname
mysql://user:pass@host:3306/dbname
sqlite:///absolute/path/to/file.db
```

The database kind is inferred from the URL scheme; set `DB_TYPE` explicitly
(`postgres` | `mysql` | `sqlite`) if that ever fails.

Tuning knobs (all optional, sane defaults): `DEFAULT_ROW_LIMIT`,
`MAX_ROW_LIMIT`, `QUERY_TIMEOUT_MS`, `SCHEMA_CACHE_TTL_MS` — see
`.env.example`.
