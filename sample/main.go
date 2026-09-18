// Command sample-gemini-agent shows how any agent can be wired up to
// generic-db-mcp: it spawns the already-built MCP server binary, hands its
// tools (list_tables, describe_table, run_query) to Gemini as function
// declarations, and lets Gemini decide when to call them to answer a
// question about the database.
//
// This file intentionally imports nothing from this repo's internal
// packages — it only talks to the MCP server binary as an external
// process, exactly like a real client would, over the same
// stdin/stdout protocol any MCP-compatible agent uses.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/genai"
)

const maxToolCallRounds = 8

func main() {
	_ = godotenv.Load() // sample/.env, if present

	ctx := context.Background()

	session, closeSession := connectMCP(ctx)
	defer closeSession()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		log.Fatalf("listing MCP tools: %v", err)
	}
	fnDecls := toFunctionDeclarations(tools.Tools)
	fmt.Printf("Connected. %d tool(s) available to the model: ", len(fnDecls))
	for i, d := range fnDecls {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(d.Name)
	}
	fmt.Println()

	geminiClient, err := genai.NewClient(ctx, nil) // reads GEMINI_API_KEY from env
	if err != nil {
		log.Fatalf("creating Gemini client: %v", err)
	}

	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-2.5-flash"
	}

	config := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(
			"You are a helpful assistant with read-only access to a SQL database "+
				"through tools. Use list_tables/describe_table to learn the schema "+
				"before writing a query, then use run_query to answer the user's "+
				"question. Only ever issue SELECT statements.",
			genai.RoleUser,
		),
		Tools: []*genai.Tool{{FunctionDeclarations: fnDecls}},
	}

	fmt.Println(`Ask a question about the database (or type "exit"):`)
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				log.Println("reading stdin:", err)
			}
			return
		}
		question := strings.TrimSpace(scanner.Text())
		if question == "" {
			continue
		}
		if question == "exit" || question == "quit" {
			return
		}

		answer, err := ask(ctx, session, geminiClient, model, config, question)
		if err != nil {
			fmt.Println("error:", err)
			continue
		}
		fmt.Println(answer)
	}
}

// connectMCP builds the path to the MCP server binary (MCP_SERVER_PATH env
// var, default ../bin/generic-db-mcp) and spawns it with its working
// directory set to the project root (MCP_SERVER_DIR, default ".."), so the
// server's own .env — the one holding DATABASE_URL — loads exactly as it
// would for any other client.
func connectMCP(ctx context.Context) (*mcp.ClientSession, func()) {
	serverPath := os.Getenv("MCP_SERVER_PATH")
	if serverPath == "" {
		serverPath = "../bin/generic-db-mcp"
	}
	serverDir := os.Getenv("MCP_SERVER_DIR")
	if serverDir == "" {
		serverDir = ".."
	}

	// Resolve to an absolute path *before* Dir is set below: the child
	// process chdir's into Dir before resolving a relative exec path, so a
	// relative serverPath would otherwise be applied a second time on top
	// of that chdir (e.g. "../bin/x" run from Dir ".." looks one directory
	// too far up).
	absServerPath, err := filepath.Abs(serverPath)
	if err != nil {
		log.Fatalf("resolving MCP server path %q: %v", serverPath, err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "sample-gemini-agent", Version: "0.1.0"}, nil)
	cmd := exec.Command(absServerPath)
	cmd.Dir = serverDir
	cmd.Stderr = os.Stderr
	transport := &mcp.CommandTransport{Command: cmd}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		log.Fatalf("connecting to MCP server %q: %v", serverPath, err)
	}
	return session, func() { session.Close() }
}

func toFunctionDeclarations(tools []*mcp.Tool) []*genai.FunctionDeclaration {
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, &genai.FunctionDeclaration{
			Name:                 t.Name,
			Description:          t.Description,
			ParametersJsonSchema: t.InputSchema,
		})
	}
	return decls
}

// ask runs the full function-calling loop for a single question: send the
// prompt to Gemini, execute whatever tool calls it asks for via MCP, feed
// the results back, and repeat until Gemini answers in plain text.
func ask(
	ctx context.Context,
	session *mcp.ClientSession,
	client *genai.Client,
	model string,
	config *genai.GenerateContentConfig,
	question string,
) (string, error) {
	contents := []*genai.Content{genai.NewContentFromText(question, genai.RoleUser)}

	for range maxToolCallRounds {
		resp, err := client.Models.GenerateContent(ctx, model, contents, config)
		if err != nil {
			return "", fmt.Errorf("gemini generate content: %w", err)
		}

		calls := resp.FunctionCalls()
		if len(calls) == 0 {
			return resp.Text(), nil
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
			return "", fmt.Errorf("gemini requested tool calls but returned no content to continue the conversation")
		}
		contents = append(contents, resp.Candidates[0].Content)

		var responseParts []*genai.Part
		for _, call := range calls {
			fmt.Printf("  [calling %s%v]\n", call.Name, call.Args)
			result, callErr := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      call.Name,
				Arguments: call.Args,
			})
			responseParts = append(responseParts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name:     call.Name,
					Response: toolResultToResponse(result, callErr),
				},
			})
		}
		contents = append(contents, &genai.Content{Role: genai.RoleUser, Parts: responseParts})
	}

	return "", fmt.Errorf("gave up after %d rounds of tool calls without a final answer", maxToolCallRounds)
}

// toolResultToResponse turns an MCP tool result into the map Gemini expects
// as a function response. Structured output from AddTool-based handlers
// (see internal/mcpserver) already arrives as a JSON object and is passed
// through as-is; anything else (including a rejected/erroring call) falls
// back to its text content under "error" or "output".
func toolResultToResponse(result *mcp.CallToolResult, err error) map[string]any {
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	if result.IsError {
		return map[string]any{"error": textContent(result)}
	}
	if m, ok := result.StructuredContent.(map[string]any); ok {
		return m
	}
	return map[string]any{"output": textContent(result)}
}

func textContent(result *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}
