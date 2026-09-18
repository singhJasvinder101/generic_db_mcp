package dbadapter

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")

	setup, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening setup db: %v", err)
	}
	defer setup.Close()

	_, err = setup.Exec(`
		create table orgs (id integer primary key, name text not null);
		create table users (id integer primary key, name text not null, org_id integer references orgs(id));
		insert into orgs (id, name) values (1, 'Acme');
		insert into users (id, name, org_id) values (1, 'Ada', 1), (2, 'Grace', 1), (3, 'Hedy', 1);
	`)
	if err != nil {
		t.Fatalf("seeding setup db: %v", err)
	}
	return path
}

func TestSQLiteListTablesFindsColumnsAndKeys(t *testing.T) {
	path := setupTestDB(t)
	adapter, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer adapter.Close()

	ctx := context.Background()
	if err := adapter.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	tables, err := adapter.ListTables(ctx)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}

	var users *Table
	for i := range tables {
		if tables[i].Name == "users" {
			users = &tables[i]
		}
	}
	if users == nil {
		t.Fatalf("users table not found")
	}

	var sawPK, sawFK bool
	for _, c := range users.Columns {
		if c.Name == "id" && c.IsPrimaryKey {
			sawPK = true
		}
		if c.Name == "org_id" && c.IsForeignKey && c.RefTable == "orgs" {
			sawFK = true
		}
	}
	if !sawPK {
		t.Errorf("expected users.id to be detected as primary key")
	}
	if !sawFK {
		t.Errorf("expected users.org_id to be detected as a foreign key to orgs")
	}
}

func TestSQLiteRunReadOnlyQueryRespectsRowLimit(t *testing.T) {
	path := setupTestDB(t)
	adapter, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer adapter.Close()

	ctx := context.Background()
	if err := adapter.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	result, err := adapter.RunReadOnlyQuery(ctx, "select * from users order by id", 2)
	if err != nil {
		t.Fatalf("RunReadOnlyQuery: %v", err)
	}
	if result.RowCount != 2 {
		t.Errorf("expected 2 rows, got %d", result.RowCount)
	}
	if !result.Truncated {
		t.Errorf("expected truncated=true when rowLimit is hit")
	}
}

func TestSQLiteFileHandleRefusesWrites(t *testing.T) {
	path := setupTestDB(t)
	adapter, err := NewSQLite(path)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer adapter.Close()

	sqliteAdapter, ok := adapter.(*sqliteAdapter)
	if !ok {
		t.Fatalf("expected *sqliteAdapter")
	}

	// Even if a mutating statement somehow reached the adapter, the
	// underlying file handle was opened with mode=ro and must refuse it.
	_, err = sqliteAdapter.db.Exec("delete from users")
	if err == nil {
		t.Fatalf("expected write to fail on a read-only file handle, but it succeeded")
	}
}
