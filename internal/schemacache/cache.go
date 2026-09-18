// Package schemacache holds a short-lived, in-memory copy of the last
// schema introspection so repeated "what tables are there" calls from an
// agent don't have to re-walk information_schema every time.
package schemacache

import (
	"sync"
	"time"

	"generic-db-mcp/internal/dbadapter"
)

type Cache struct {
	ttl       time.Duration
	mu        sync.Mutex
	tables    []dbadapter.Table
	fetchedAt time.Time
	valid     bool
}

func New(ttl time.Duration) *Cache {
	return &Cache{ttl: ttl}
}

func (c *Cache) Get() ([]dbadapter.Table, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || time.Since(c.fetchedAt) > c.ttl {
		return nil, false
	}
	return c.tables, true
}

func (c *Cache) Set(tables []dbadapter.Table) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tables = tables
	c.fetchedAt = time.Now()
	c.valid = true
}

func (c *Cache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valid = false
}
