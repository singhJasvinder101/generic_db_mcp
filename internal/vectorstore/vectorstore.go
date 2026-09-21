// Package vectorstore holds business-context notes about a database's
// tables/columns (seeded via the teach_schema_context tool) and finds the
// ones relevant to a natural-language question, so an agent isn't left
// guessing what a table actually means from its name alone. Store is a
// pluggable interface — the only implementation today is an in-memory,
// file-persisted store, sized for the hundreds-of-tables/columns a typical
// schema has; a client that needs it can later swap in a dedicated vector
// database (pgvector, Qdrant, ...) behind the same interface.
package vectorstore

import "context"

// Document is one piece of taught context, scoped to a table or a specific
// column within it.
type Document struct {
	ID        string    `json:"id"`
	Schema    string    `json:"schema,omitempty"`
	Table     string    `json:"table"`
	Column    string    `json:"column,omitempty"`
	Text      string    `json:"text"`
	Embedding []float32 `json:"embedding"`
}

// Match is a Document ranked by similarity to a query.
type Match struct {
	Document
	Score float32 `json:"score"`
}

type Store interface {
	// Upsert adds or replaces documents, keyed by Document.ID.
	Upsert(ctx context.Context, docs []Document) error
	// Query returns up to topK documents most similar to queryEmbedding,
	// most similar first.
	Query(ctx context.Context, queryEmbedding []float32, topK int) ([]Match, error)
}
