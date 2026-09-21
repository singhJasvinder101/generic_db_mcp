package vectorstore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
)

// memoryStore keeps every document in memory and does a brute-force cosine
// similarity scan on Query. That's the right trade-off here: a schema's
// worth of context notes is at most a few thousand documents, small enough
// that a linear scan is microseconds, so there's no need for an index or an
// external service. It persists to a single JSON file after every write so
// taught context survives a restart.
type memoryStore struct {
	path string
	mu   sync.Mutex
	docs map[string]Document
}

// NewMemory returns a Store that persists to path. Pass an empty path for a
// purely in-memory store (context is lost on restart) — mainly useful for
// tests.
func NewMemory(path string) (Store, error) {
	s := &memoryStore{path: path, docs: map[string]Document{}}
	if path == "" {
		return s, nil
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *memoryStore) load() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("loading vector store file %q: %w", s.path, err)
	}
	var docs []Document
	if err := json.Unmarshal(data, &docs); err != nil {
		return fmt.Errorf("parsing vector store file %q: %w", s.path, err)
	}
	for _, d := range docs {
		s.docs[d.ID] = d
	}
	return nil
}

// persist assumes the caller already holds s.mu.
func (s *memoryStore) persist() error {
	if s.path == "" {
		return nil
	}
	docs := make([]Document, 0, len(s.docs))
	for _, d := range s.docs {
		docs = append(docs, d)
	}
	data, err := json.MarshalIndent(docs, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding vector store: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("writing vector store file %q: %w", s.path, err)
	}
	return nil
}

func (s *memoryStore) Upsert(ctx context.Context, docs []Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, d := range docs {
		if d.ID == "" {
			return fmt.Errorf("document is missing an ID")
		}
		s.docs[d.ID] = d
	}
	return s.persist()
}

func (s *memoryStore) Query(ctx context.Context, queryEmbedding []float32, topK int) ([]Match, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	matches := make([]Match, 0, len(s.docs))
	for _, d := range s.docs {
		matches = append(matches, Match{Document: d, Score: cosineSimilarity(queryEmbedding, d.Embedding)})
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	if topK > 0 && len(matches) > topK {
		matches = matches[:topK]
	}
	return matches, nil
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}
