package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

type DB struct {
	sql  *sql.DB
	kind string // "mysql"
}

func OpenDB(dsn string) (*DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	// Verify connection
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return &DB{sql: db, kind: "mysql"}, nil
}

func (d *DB) Close() { _ = d.sql.Close() }

// Query runs SQL and returns column names and [][]any rows
func (d *DB) Query(ctx context.Context, sqlText string) ([]string, [][]any, error) {
	rows, err := d.sql.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	out := make([][]any, 0, 64)
	ptrs := make([]any, len(cols))
	vals := make([]any, len(cols))

	for i := range vals {
		ptrs[i] = &vals[i]
	}

	for rows.Next() {
		for i := range vals {
			vals[i] = nil
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make([]any, len(cols))
		for i, v := range vals {
			row[i] = normalizeDBValue(v)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return cols, out, nil
}

// Exec is optional (for insert/update paths if you extend)
func (d *DB) Exec(ctx context.Context, sqlText string) (int64, error) {
	res, err := d.sql.ExecContext(ctx, sqlText)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func normalizeDBValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return v
	}
}

var ErrNoSQL = errors.New("transformer returned empty SQL")

func MustNonEmpty(sql string) (string, error) {
	if sql == "" {
		return "", ErrNoSQL
	}
	return sql, nil
}

func DSNExample() string {
	return fmt.Sprintf("user:pass@tcp(127.0.0.1:3306)/graph?parseTime=true&loc=Local")
}
