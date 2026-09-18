# sample-gemini-agent

A minimal example of wiring **any** LLM agent up to `generic-db-mcp`. This
one uses Gemini's function calling, but the pattern is the same regardless
of which model/agent framework you use:

1. Spawn the MCP server binary as a subprocess (stdio transport).
2. Ask it for its tools (`list_tables`, `describe_table`, `run_query`) —
   each one already carries a JSON Schema describing its arguments.
3. Hand those schemas to the model as function declarations.
4. When the model asks to call one, forward the call to the MCP server and
   feed the result back. Repeat until the model answers in plain text.

This file only talks to the server over that stdio protocol — it does not
import anything from the rest of this repo, exactly like a real external
client wouldn't.

## Setup

From the project root, make sure the server binary exists:

```bash
go build -o bin/generic-db-mcp .
```

Its `.env` (with `DATABASE_URL`) should sit next to it in the project root
— see `../.env.example`. The sample spawns the server with the project
root as its working directory, so the server loads that `.env` itself.

Then, in this directory:

```bash
cp .env.example .env   # add your GEMINI_API_KEY
go run .
```

## Usage

```
Connected. 3 tool(s) available to the model: list_tables, describe_table, run_query
Ask a question about the database (or type "exit"):
> what's the current subscription for the org named jassi?
  [calling list_tables map[]]
  [calling run_query map[sql:select ...]]
Jassi is on an active Enterprise plan running until 2027-04-06...
```

## Adapting this to your own agent/framework

Whatever you're building, the shape is always: **list MCP tools → convert
their JSON Schema into your framework's tool format → call them through
the MCP client session when the model asks**. Swap out `client.Models.GenerateContent`
and the `genai.*` types in `main.go` for your framework's equivalents; the
`connectMCP`, `toFunctionDeclarations`, and `toolResultToResponse` helpers
stay basically the same for any MCP-compatible agent.
