// Package httpserver exposes an *mcp.Server over Streamable HTTP instead of
// stdio, for the multi-tenant/multi-agent case: many concurrent MCP client
// sessions (potentially from many of a company's own internal agents) share
// one running process and therefore one database connection pool, instead
// of each session spawning its own OS process with its own pool. The
// database adapter's pool sizing (internal/dbadapter) already assumes a
// single shared pool serving concurrent queries, so no changes were needed
// there — this package only adds the network listener on top.
package httpserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const shutdownTimeout = 5 * time.Second

// Run serves server over Streamable HTTP at addr until ctx is cancelled,
// then shuts down gracefully. Every incoming session is handed the same
// server instance (mcp.NewStreamableHTTPHandler explicitly allows this),
// which is what makes the shared connection pool possible: every tool call,
// from every client, runs against the one adapter the server was built
// with.
//
// If authToken is non-empty, requests must carry a matching
// "Authorization: Bearer <authToken>" header. Running without a token
// exposes read access to the database to anyone who can reach addr, so it
// should only be left empty behind another access control layer (a VPN, a
// reverse proxy that adds auth, a private network).
func Run(ctx context.Context, addr string, authToken string, server *mcp.Server) error {
	getServer := func(*http.Request) *mcp.Server { return server }
	var handler http.Handler = mcp.NewStreamableHTTPHandler(getServer, nil)

	if authToken != "" {
		handler = requireBearerToken(authToken, handler)
	} else {
		log.Printf("WARNING: MCP_HTTP_AUTH_TOKEN is not set; %s is reachable, unauthenticated, by anyone who can reach it", addr)
	}

	httpServer := &http.Server{Addr: addr, Handler: handler}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()
	log.Printf("MCP server ready on http://%s (streamable HTTP, one shared connection pool across all sessions)", addr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func requireBearerToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
