package dbadapter

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
)

type mysqlAdapter struct {
	db *sql.DB
}

func NewMySQL(connString string) (Adapter, error) {
	dsn, err := mysqlURLToDSN(connString)
	if err != nil {
		return nil, fmt.Errorf("parsing mysql connection string: %w", err)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening mysql connection: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(5)

	return &mysqlAdapter{db: db}, nil
}

// mysqlURLToDSN turns a familiar mysql://user:pass@host:port/dbname URL into
// the driver-native DSN format go-sql-driver/mysql expects.
func mysqlURLToDSN(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	cfg := mysqldriver.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	cfg.ParseTime = true
	if u.User != nil {
		cfg.User = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			cfg.Passwd = pw
		}
	}
	return cfg.FormatDSN(), nil
}

func (a *mysqlAdapter) Kind() Kind { return MySQL }

func (a *mysqlAdapter) Connect(ctx context.Context) error {
	return a.db.PingContext(ctx)
}

func (a *mysqlAdapter) Close() error {
	return a.db.Close()
}

const mysqlColumnsQuery = `
select
	c.table_schema as table_schema,
	c.table_name as table_name,
	c.column_name as column_name,
	c.data_type as data_type,
	c.is_nullable = 'YES' as nullable,
	c.column_default as default_value,
	c.column_key = 'PRI' as is_primary_key,
	kcu.referenced_table_name as foreign_table,
	kcu.referenced_column_name as foreign_column
from information_schema.columns c
left join information_schema.key_column_usage kcu
	on kcu.table_schema = c.table_schema
	and kcu.table_name = c.table_name
	and kcu.column_name = c.column_name
	and kcu.referenced_table_name is not null
where c.table_schema = database()
order by c.table_schema, c.table_name, c.ordinal_position`

func (a *mysqlAdapter) ListTables(ctx context.Context) ([]Table, error) {
	rows, err := a.db.QueryContext(ctx, mysqlColumnsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := []string{}
	byKey := map[string]*Table{}

	for rows.Next() {
		var schema, name, colName, dataType string
		var nullable, isPK bool
		var defaultValue *string
		var foreignTable, foreignColumn *string

		if err := rows.Scan(&schema, &name, &colName, &dataType, &nullable, &defaultValue, &isPK, &foreignTable, &foreignColumn); err != nil {
			return nil, err
		}

		key := schema + "." + name
		table, ok := byKey[key]
		if !ok {
			table = &Table{Schema: schema, Name: name}
			byKey[key] = table
			order = append(order, key)
		}

		col := Column{
			Name:         colName,
			DataType:     dataType,
			Nullable:     nullable,
			IsPrimaryKey: isPK,
			Default:      defaultValue,
		}
		if foreignTable != nil && foreignColumn != nil {
			col.IsForeignKey = true
			col.RefTable = *foreignTable
			col.RefColumn = *foreignColumn
		}
		table.Columns = append(table.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tables := make([]Table, 0, len(order))
	for _, key := range order {
		tables = append(tables, *byKey[key])
	}
	return tables, nil
}

func (a *mysqlAdapter) DescribeTable(ctx context.Context, table, schema string) (*Table, error) {
	tables, err := a.ListTables(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range tables {
		if t.Name == table && (schema == "" || t.Schema == schema) {
			return &t, nil
		}
	}
	return nil, nil
}

func (a *mysqlAdapter) RunReadOnlyQuery(ctx context.Context, sqlText string, rowLimit int) (*QueryResult, error) {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("beginning read-only transaction: %w", err)
	}
	defer tx.Rollback()

	wrapped := fmt.Sprintf("select * from (%s) as _generic_db_mcp_subquery limit %d", sqlText, rowLimit)
	rows, err := tx.QueryContext(ctx, wrapped)
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
