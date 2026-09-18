package dbadapter

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type sqliteAdapter struct {
	db *sql.DB
}

// NewSQLite opens the database file in SQLite's own read-only URI mode
// (mode=ro), so even if a query somehow got past the SQL guard, the OS-level
// file handle itself cannot perform a write.
func NewSQLite(filePath string) (Adapter, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", filePath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}
	db.SetMaxOpenConns(4)
	return &sqliteAdapter{db: db}, nil
}

func (a *sqliteAdapter) Kind() Kind { return SQLite }

func (a *sqliteAdapter) Connect(ctx context.Context) error {
	return a.db.PingContext(ctx)
}

func (a *sqliteAdapter) Close() error {
	return a.db.Close()
}

func (a *sqliteAdapter) ListTables(ctx context.Context) ([]Table, error) {
	tableRows, err := a.db.QueryContext(ctx,
		"select name from sqlite_master where type = 'table' and name not like 'sqlite_%'")
	if err != nil {
		return nil, err
	}
	var names []string
	for tableRows.Next() {
		var name string
		if err := tableRows.Scan(&name); err != nil {
			tableRows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	if err := tableRows.Err(); err != nil {
		return nil, err
	}
	tableRows.Close()

	tables := make([]Table, 0, len(names))
	for _, name := range names {
		table, err := a.describeByName(ctx, name)
		if err != nil {
			return nil, err
		}
		tables = append(tables, *table)
	}
	return tables, nil
}

func (a *sqliteAdapter) describeByName(ctx context.Context, name string) (*Table, error) {
	// PRAGMA doesn't accept bound parameters; the name comes from
	// sqlite_master itself (never user input), so this is safe.
	colRows, err := a.db.QueryContext(ctx, fmt.Sprintf("pragma table_info(%q)", name))
	if err != nil {
		return nil, err
	}
	defer colRows.Close()

	type pragmaCol struct {
		name    string
		colType string
		notNull bool
		pk      int
		dflt    *string
	}
	var cols []pragmaCol
	for colRows.Next() {
		var cid int
		var c pragmaCol
		if err := colRows.Scan(&cid, &c.name, &c.colType, &c.notNull, &c.dflt, &c.pk); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}

	if err := colRows.Err(); err != nil {
		return nil, err
	}

	fkRows, err := a.db.QueryContext(ctx, fmt.Sprintf("pragma foreign_key_list(%q)", name))
	if err != nil {
		return nil, err
	}
	defer fkRows.Close()

	fkByColumn := map[string][2]string{} // column -> [refTable, refColumn]
	for fkRows.Next() {
		var id, seq int
		var refTable, from, to string
		var onUpdate, onDelete, match string
		if err := fkRows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		fkByColumn[from] = [2]string{refTable, to}
	}
	if err := fkRows.Err(); err != nil {
		return nil, err
	}

	table := &Table{Name: name}
	for _, c := range cols {
		col := Column{
			Name:         c.name,
			DataType:     c.colType,
			Nullable:     !c.notNull,
			IsPrimaryKey: c.pk > 0,
			Default:      c.dflt,
		}
		if fk, ok := fkByColumn[c.name]; ok {
			col.IsForeignKey = true
			col.RefTable = fk[0]
			col.RefColumn = fk[1]
		}
		table.Columns = append(table.Columns, col)
	}
	return table, nil
}

func (a *sqliteAdapter) DescribeTable(ctx context.Context, table, _ string) (*Table, error) {
	tables, err := a.ListTables(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tables {
		if t.Name == table {
			return &t, nil
		}
	}
	return nil, nil
}

func (a *sqliteAdapter) RunReadOnlyQuery(ctx context.Context, sqlText string, rowLimit int) (*QueryResult, error) {
	wrapped := fmt.Sprintf("select * from (%s) as _generic_db_mcp_subquery limit %d", sqlText, rowLimit)
	rows, err := a.db.QueryContext(ctx, wrapped)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var resultRows []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		scanArgs := make([]any, len(columns))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			row[col] = normalizeValue(values[i])
		}
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &QueryResult{
		Columns:   columns,
		Rows:      resultRows,
		RowCount:  len(resultRows),
		Truncated: len(resultRows) >= rowLimit,
	}, nil
}
