package vectorstore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestQueryRanksBySimilarity(t *testing.T) {
	store, err := NewMemory("")
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	ctx := context.Background()

	docs := []Document{
		{ID: "orders", Table: "orders", Text: "customer orders", Embedding: []float32{1, 0, 0}},
		{ID: "logs", Table: "logs", Text: "audit logs", Embedding: []float32{0, 1, 0}},
	}
	if err := store.Upsert(ctx, docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	matches, err := store.Query(ctx, []float32{1, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].ID != "orders" {
		t.Errorf("expected %q to rank first, got %q (score %v)", "orders", matches[0].ID, matches[0].Score)
	}
	if matches[0].Score <= matches[1].Score {
		t.Errorf("expected orders' score (%v) to beat logs' score (%v)", matches[0].Score, matches[1].Score)
	}
}

func TestQueryRespectsTopK(t *testing.T) {
	store, err := NewMemory("")
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		if err := store.Upsert(ctx, []Document{{ID: id, Table: id, Embedding: []float32{1, 0}}}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	matches, err := store.Query(ctx, []float32{1, 0}, 2)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected topK=2 to cap results at 2, got %d", len(matches))
	}
}

func TestUpsertReplacesByID(t *testing.T) {
	store, err := NewMemory("")
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	ctx := context.Background()

	if err := store.Upsert(ctx, []Document{{ID: "t", Table: "t", Text: "first", Embedding: []float32{1, 0}}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := store.Upsert(ctx, []Document{{ID: "t", Table: "t", Text: "second", Embedding: []float32{1, 0}}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	matches, err := store.Query(ctx, []float32{1, 0}, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected re-upserting the same ID to replace, not duplicate; got %d docs", len(matches))
	}
	if matches[0].Text != "second" {
		t.Errorf("expected replaced text %q, got %q", "second", matches[0].Text)
	}
}

func TestPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context.json")
	ctx := context.Background()

	store, err := NewMemory(path)
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	if err := store.Upsert(ctx, []Document{{ID: "t", Table: "t", Text: "note", Embedding: []float32{1, 0}}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	reloaded, err := NewMemory(path)
	if err != nil {
		t.Fatalf("NewMemory (reload): %v", err)
	}
	matches, err := reloaded.Query(ctx, []float32{1, 0}, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 1 || matches[0].Text != "note" {
		t.Fatalf("expected persisted document to survive reload, got %+v", matches)
	}
}
