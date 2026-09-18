package dbadapter

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresAdapter struct {
	pool *pgxpool.Pool
}

func NewPostgres(connString string) (Adapter, error) {
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("parsing postgres connection string: %w", err)
	}
	cfg.MaxConns = 5

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("configuring postgres pool: %w", err)
	}
	return &postgresAdapter{pool: pool}, nil
}

func (a *postgresAdapter) Kind() Kind { return Postgres }

func (a *postgresAdapter) Connect(ctx context.Context) error {
	return a.pool.Ping(ctx)
}

func (a *postgresAdapter) Close() error {
	a.pool.Close()
	return nil
}

const postgresColumnsQuery = `
select
	c.table_schema as schema,
	c.table_name as name,
	c.column_name as column_name,
	c.data_type as data_type,
	c.is_nullable = 'YES' as nullable,
	c.column_default as default_value,
	exists (
		select 1 from information_schema.table_constraints tc
		join information_schema.key_column_usage kcu
			on tc.constraint_name = kcu.constraint_name and tc.table_schema = kcu.table_schema
		where tc.constraint_type = 'PRIMARY KEY'
			and tc.table_schema = c.table_schema and tc.table_name = c.table_name
			and kcu.column_name = c.column_name
	) as is_primary_key,
	fk.foreign_table, fk.foreign_column
from information_schema.columns c
left join (
	select
		kcu.table_schema, kcu.table_name, kcu.column_name,
		ccu.table_name as foreign_table, ccu.column_name as foreign_column
	from information_schema.table_constraints tc
	join information_schema.key_column_usage kcu
		on tc.constraint_name = kcu.constraint_name and tc.table_schema = kcu.table_schema
	join information_schema.constraint_column_usage ccu
		on tc.constraint_name = ccu.constraint_name and tc.table_schema = ccu.table_schema
	where tc.constraint_type = 'FOREIGN KEY'
) fk on fk.table_schema = c.table_schema and fk.table_name = c.table_name and fk.column_name = c.column_name
where c.table_schema not in ('pg_catalog', 'information_schema')
order by c.table_schema, c.table_name, c.ordinal_position`

func (a *postgresAdapter) ListTables(ctx context.Context) ([]Table, error) {
	rows, err := a.pool.Query(ctx, postgresColumnsQuery)
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

func (a *postgresAdapter) DescribeTable(ctx context.Context, table, schema string) (*Table, error) {
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

func (a *postgresAdapter) RunReadOnlyQuery(ctx context.Context, sql string, rowLimit int) (*QueryResult, error) {
	tx, err := a.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("beginning read-only transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	wrapped := fmt.Sprintf("select * from (%s) as _generic_db_mcp_subquery limit %d", sql, rowLimit)
	rows, err := tx.Query(ctx, wrapped)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	columns := make([]string, len(fields))
	for i, f := range fields {
		columns[i] = f.Name
	}

	var resultRows []map[string]any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
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

func normalizeValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case [16]byte:
		// pgx decodes uuid columns into a raw [16]byte rather than a
		// pgtype.UUID, so without this they'd serialize as a JSON array of
		// 16 numbers instead of a normal UUID string.
		return fmt.Sprintf("%x-%x-%x-%x-%x", t[0:4], t[4:6], t[6:8], t[8:10], t[10:16])
	default:
		return v
	}
}
