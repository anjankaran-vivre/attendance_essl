package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/microsoft/go-mssqldb"
)

type QueryResult struct {
	Columns []string
	Rows    [][]any
}

type MSSQLDatabase struct {
	connStr string
	db      *sql.DB
}

func NewMSSQLDatabase(connStr string) *MSSQLDatabase {
	return &MSSQLDatabase{connStr: connStr}
}

func (m *MSSQLDatabase) Connect() error {
	db, err := sql.Open("sqlserver", m.connStr)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}

	m.db = db
	slog.Info("connected to MSSQL database")
	return nil
}

func (m *MSSQLDatabase) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

func (m *MSSQLDatabase) ExecuteQuery(query string, args ...any) (*QueryResult, error) {
	if m.db == nil {
		return nil, fmt.Errorf("database not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query execution error: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("row scan error: %w", err)
		}

		row := make([]any, len(columns))
		for i, v := range values {
			row[i] = v
		}
		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	slog.Info("query executed", "rows", len(result))
	return &QueryResult{Columns: columns, Rows: result}, nil
}

func (m *MSSQLDatabase) QueryToSlice(query string) ([]string, [][]any, error) {
	res, err := m.ExecuteQuery(query)
	if err != nil {
		return nil, nil, err
	}
	return res.Columns, res.Rows, nil
}

func (m *MSSQLDatabase) ListDatabases() ([]string, error) {
	res, err := m.ExecuteQuery("SELECT name FROM sys.databases ORDER BY name")
	if err != nil {
		return nil, err
	}
	var databases []string
	for _, row := range res.Rows {
		if len(row) > 0 {
			databases = append(databases, fmt.Sprintf("%v", row[0]))
		}
	}
	return databases, nil
}

func (m *MSSQLDatabase) GetTablesList() ([]string, error) {
	query := `
		SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE
		FROM INFORMATION_SCHEMA.TABLES
		ORDER BY TABLE_TYPE, TABLE_SCHEMA, TABLE_NAME
	`
	res, err := m.ExecuteQuery(query)
	if err != nil {
		return nil, err
	}
	var tables []string
	for _, row := range res.Rows {
		schema := "dbo"
		name := ""
		typ := ""
		if len(row) > 0 {
			schema = fmt.Sprintf("%v", row[0])
		}
		if len(row) > 1 {
			name = fmt.Sprintf("%v", row[1])
		}
		if len(row) > 2 {
			typ = fmt.Sprintf("%v", row[2])
		}
		entry := fmt.Sprintf("%s.%s", schema, name)
		if typ == "VIEW" {
			entry += " (VIEW)"
		}
		tables = append(tables, entry)
	}
	return tables, nil
}

func (m *MSSQLDatabase) GetTableSchema(tableName string) (*QueryResult, error) {
	query := `
		SELECT
			COLUMN_NAME,
			DATA_TYPE,
			CHARACTER_MAXIMUM_LENGTH,
			IS_NULLABLE
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_NAME = @p1
	`
	return m.ExecuteQuery(query, tableName)
}

func (m *MSSQLDatabase) ExecuteNonQuery(query string, args ...any) error {
	if m.db == nil {
		return fmt.Errorf("database not connected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := m.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("execute error: %w", err)
	}
	return nil
}

func (m *MSSQLDatabase) TableExists(tableName string) (bool, error) {
	query := `SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_NAME = @p1`
	res, err := m.ExecuteQuery(query, tableName)
	if err != nil {
		return false, err
	}
	if len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
		count := 0
		fmt.Sscanf(fmt.Sprintf("%v", res.Rows[0][0]), "%d", &count)
		return count > 0, nil
	}
	return false, nil
}

func (m *MSSQLDatabase) CreateDatabaseIfNotExists(name string) error {
	q := fmt.Sprintf("IF NOT EXISTS (SELECT name FROM sys.databases WHERE name = N'%s') CREATE DATABASE [%s]", name, name)
	return m.ExecuteNonQuery(q)
}

func (m *MSSQLDatabase) TestConnection() bool {
	if m.db == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := m.db.PingContext(ctx); err != nil {
		slog.Error("connection test failed", "error", err)
		return false
	}
	return true
}
