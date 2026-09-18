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

## Wiring it into an agent

Any MCP client that can launch a stdio server works. For Claude Code /
Claude Desktop, add to your MCP config:

```json
{
  "mcpServers": {
    "generic-db-mcp": {
      "command": "/absolute/path/to/bin/generic-db-mcp",
      "env": { "DATABASE_URL": "postgres://user:pass@host:5432/dbname" }
    }
  }
}
```

## Distributing to a client

A client only needs two things, nothing else from this repo:

1. The compiled binary for their OS (`GOOS=... GOARCH=... go build -o generic-db-mcp .`).
2. A `.env` next to it with their own `DATABASE_URL`.

They point their own agent/LLM at that binary (see the MCP config snippet
above) — no source code hand-off required. See [`sample/`](sample/) for a
worked example of wiring a non-MCP-native agent (Gemini, via function
calling) up to this server.

## Security notes

- The SQL guard is a defense-in-depth measure, not a substitute for
  least-privilege database credentials. **Always point `DATABASE_URL` at a
  read-only database user/role** — the guard and the read-only transaction
  are both belt-and-suspenders on top of that, not instead of it.
- `run_query` accepts exactly one statement; stacked statements
  (`SELECT ...; DROP ...`) are rejected before they reach the driver.

## Development

```bash
go build ./...
go vet ./...
go test ./...
```
