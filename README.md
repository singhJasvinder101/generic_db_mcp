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
| `teach_schema_context`* | Record what a table/column actually means in plain language — for when names are ambiguous or misleading. |
| `search_schema_context`* | Semantic search over notes saved with `teach_schema_context`, so an agent can check for human-provided context before querying an unfamiliar table. |

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

## Schema business context

Table and column names don't always say what they mean — a table named
`tbl2` might actually hold orders, or a `status` column's values might only
make sense with business context a schema can't express. `teach_schema_context`
lets a human (or the agent itself, once told) record that meaning in plain
language; `search_schema_context` retrieves the relevant notes for a given
question via semantic search, so future queries don't have to guess from
naming alone.

This is off by default. To turn it on:

1. Install [Ollama](https://ollama.com) and pull the embedding model (small,
   multilingual, runs on CPU):

   ```bash
   ollama pull bge-m3
   ```

   Nothing about your schema or data leaves the machine — embeddings are
   computed by this local Ollama server.

2. Add to your `.env` (or set as real env vars):

   ```bash
   SCHEMA_CONTEXT_ENABLED=true
   # everything below is optional, these are the defaults:
   # OLLAMA_BASE_URL=http://localhost:11434
   # EMBEDDING_MODEL=bge-m3
   # VECTOR_STORE_PATH=schema_context.json
   ```

3. Restart the server. `teach_schema_context` and `search_schema_context`
   now show up in the tool list for any connected agent — nothing else to
   run. Notes persist to the JSON file at `VECTOR_STORE_PATH`.

From there it's just agent tool calls, no separate CLI:

```
teach_schema_context({
  table: "tbl2",
  description: "This actually stores customer orders, not users. Renamed from 'orders_v2' during a migration that never finished.",
  aliases: ["orders", "orders_v2"]
})

search_schema_context({ query: "where do we keep customer orders?" })
```

The embedding provider and the vector store are both pluggable
(`internal/embeddings`, `internal/vectorstore`) behind an interface, so a
client that outgrows the in-memory store can swap in a dedicated vector
database later without changing the tools.

## Wiring it into an agent

Any MCP client that can launch a stdio server works. For Claude Code /
Claude Desktop, add to your MCP config:

```json
{
  "mcpServers": {
    "generic-db-mcp": {
      "command": "/absolute/path/to/bin/generic-db-mcp",
      "env": {
        "DATABASE_URL": "postgres://user:pass@host:5432/dbname",
        "SCHEMA_CONTEXT_ENABLED": "true"
      }
    }
  }
}
```

The client spawns this process itself, so whatever it needs — `DATABASE_URL`,
and `SCHEMA_CONTEXT_ENABLED`/`OLLAMA_BASE_URL`/etc. if you're using schema
context — has to be in this `env` block (or in a `.env` file sitting next to
the binary, which it also reads on startup). Leave `SCHEMA_CONTEXT_ENABLED`
out entirely if you're not using that feature.

## Running at scale: stdio vs HTTP

By default (`MCP_TRANSPORT` unset or `stdio`) each client spawns its own
copy of this process, each with its own connection pool. That's the right
model for one agent per user (Claude Desktop/Code, Cursor). It stops being
the right model once *many* of a client's own agents/users need to hit the
same database concurrently — 100 stdio sessions means 100 processes and up
to 100× the pool size, which will exhaust the database's connection limit
long before it exhausts the box's CPU.

For that case, run one long-lived server that every session shares:

```bash
MCP_TRANSPORT=http MCP_HTTP_ADDR=:8080 MCP_HTTP_AUTH_TOKEN=<a-long-random-secret> \
  ./bin/generic-db-mcp
```

Every MCP session that connects over HTTP is handed the *same* server
instance, so they all run against the one connection pool this process
opened — no per-session multiplication. Point any number of agents at
`http://host:8080` (with `Authorization: Bearer <token>`) instead of
spawning the binary themselves.

`MCP_HTTP_AUTH_TOKEN` is a minimal bearer-token gate — set it, and put the
server behind a VPN/private network/reverse-proxy auth in front of it too.
Running it open on a reachable address with no token gives anyone who can
reach it read access to the database.

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
