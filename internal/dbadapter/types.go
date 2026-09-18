package dbadapter

import "context"

type Kind string

const (
	Postgres Kind = "postgres"
	MySQL    Kind = "mysql"
	SQLite   Kind = "sqlite"
)

type Column struct {
	Name         string  `json:"name"`
	DataType     string  `json:"dataType"`
	Nullable     bool    `json:"nullable"`
	IsPrimaryKey bool    `json:"isPrimaryKey"`
	IsForeignKey bool    `json:"isForeignKey"`
	RefTable     string  `json:"refTable,omitempty"`
	RefColumn    string  `json:"refColumn,omitempty"`
	Default      *string `json:"default,omitempty"`
}

type Table struct {
	Schema  string   `json:"schema,omitempty"`
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}

type QueryResult struct {
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	RowCount  int              `json:"rowCount"`
	Truncated bool             `json:"truncated"`
}

type Adapter interface {
	Kind() Kind
	Connect(ctx context.Context) error
	Close() error
	ListTables(ctx context.Context) ([]Table, error)
	DescribeTable(ctx context.Context, table, schema string) (*Table, error)
	RunReadOnlyQuery(ctx context.Context, sql string, rowLimit int) (*QueryResult, error)
}
