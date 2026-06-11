package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/microsoft/go-mssqldb"
)

type DBConfig struct {
	DBHost     string
	DBServer   string
	DBName     string
	DBUsername string
	DBPassword string
}

type DatabaseManager struct {
	db  *sql.DB
	cfg DBConfig
}

var (
	empInstance *DatabaseManager
	empOnce     sync.Once
)

func InitEMP(cfg DBConfig) error {
	var initErr error

	empOnce.Do(func() {
		dsn := fmt.Sprintf(
			"sqlserver://@%s?instance=%s&database=%s&trusted_connection=yes&TrustServerCertificate=true",
			cfg.DBHost, cfg.DBServer, cfg.DBName,
		)

		db, err := sql.Open("sqlserver", dsn)
		if err != nil {
			initErr = fmt.Errorf("sql.Open failed: %w", err)
			return
		}

		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(10)
		db.SetConnMaxLifetime(3600 * time.Second)
		db.SetConnMaxIdleTime(600 * time.Second)

		if err := db.Ping(); err != nil {
			initErr = fmt.Errorf("db.Ping failed: %w", err)
			return
		}

		empInstance = &DatabaseManager{db: db, cfg: cfg}
		log.Printf("[EMP] Connected to %s on %s/%s", cfg.DBName, cfg.DBHost, cfg.DBServer)

		if err := empInstance.InitTables(); err != nil {
			initErr = fmt.Errorf("initTables failed: %w", err)
			return
		}
	})

	return initErr
}

func GetEMP() *DatabaseManager {
	if empInstance == nil {
		panic("[EMP] GetEMP called before InitEMP")
	}
	return empInstance
}

func (m *DatabaseManager) DB() *sql.DB {
	return m.db
}

func (m *DatabaseManager) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

func (m *DatabaseManager) InitTables() error {
	tables := []struct {
		name  string
		query string
	}{
		{
			"EmployeeDetails",
			`IF NOT EXISTS (
				SELECT 1 FROM sysobjects WHERE name = 'EmployeeDetails' AND xtype = 'U'
			)
			BEGIN
				CREATE TABLE EmployeeDetails (
					ZohoID NVARCHAR(100) PRIMARY KEY,
					FirstName NVARCHAR(255) DEFAULT '',
					LastName NVARCHAR(255) DEFAULT '',
					EmailID NVARCHAR(255) DEFAULT '',
					Mobile NVARCHAR(50) DEFAULT '',
					Designation NVARCHAR(255) DEFAULT '',
					Department NVARCHAR(255) DEFAULT '',
					ReportingToID NVARCHAR(100) DEFAULT '',
					ReportingTo NVARCHAR(255) DEFAULT '',
					SecondReportingToID NVARCHAR(100) DEFAULT '',
					SecondReportingTo NVARCHAR(255) DEFAULT '',
					Photo NVARCHAR(500) DEFAULT '',
					EmployeeID NVARCHAR(50) DEFAULT '',
					EmployeeStatus NVARCHAR(50) DEFAULT '',
					Team NVARCHAR(255) DEFAULT '',
					LocationName NVARCHAR(255) DEFAULT '',
					UpdatedAt DATETIME DEFAULT GETDATE()
				);
			END`,
		},
		{
			"Duplicate_Punches",
			`IF NOT EXISTS (
				SELECT 1 FROM sysobjects WHERE name = 'Duplicate_Punches' AND xtype = 'U'
			)
			BEGIN
				CREATE TABLE Duplicate_Punches (
					ID INT IDENTITY(1,1) PRIMARY KEY,
					UserID NVARCHAR(50),
					FirstPunchTime DATETIME,
					FirstDevice NVARCHAR(255),
					FirstDirection NVARCHAR(10),
					SecondPunchTime DATETIME,
					SecondDevice NVARCHAR(255),
					SecondDirection NVARCHAR(10),
					MinutesGap INT,
					Details NVARCHAR(MAX),
					CreatedAt DATETIME DEFAULT GETDATE()
				);
			END`,
		},
		{
			"Long_Breaks",
			`IF NOT EXISTS (
				SELECT 1 FROM sysobjects WHERE name = 'Long_Breaks' AND xtype = 'U'
			)
			BEGIN
				CREATE TABLE Long_Breaks (
					ID INT IDENTITY(1,1) PRIMARY KEY,
					UserID NVARCHAR(50),
					OutPunchTime DATETIME,
					OutDirection NVARCHAR(10),
					ReturnPunchTime DATETIME,
					ReturnDirection NVARCHAR(10),
					Device NVARCHAR(255),
					MinutesGap INT,
					Details NVARCHAR(MAX),
					CreatedAt DATETIME DEFAULT GETDATE()
				);
			END`,
		},
		{
			"Out_Reminders",
			`IF NOT EXISTS (
				SELECT 1 FROM sysobjects WHERE name = 'Out_Reminders' AND xtype = 'U'
			)
			BEGIN
				CREATE TABLE Out_Reminders (
					ID INT IDENTITY(1,1) PRIMARY KEY,
					UserID NVARCHAR(50),
					EmailID NVARCHAR(255) DEFAULT '',
					EmployeeName NVARCHAR(255) DEFAULT '',
					OutTime DATETIME,
					Device NVARCHAR(255),
					Minutes INT,
					AlertType NVARCHAR(50),
					Details NVARCHAR(MAX),
					CreatedAt DATETIME DEFAULT GETDATE()
				);
			END`,
		},
	}

	for _, t := range tables {
		if _, err := m.db.Exec(t.query); err != nil {
			return fmt.Errorf("create table %s: %w", t.name, err)
		}
		log.Printf("[EMP] Table '%s' verified", t.name)
	}

	migrations := []string{
		`IF NOT EXISTS (SELECT 1 FROM syscolumns WHERE id=OBJECT_ID('Duplicate_Punches') AND name='EmailID')
			ALTER TABLE Duplicate_Punches ADD EmailID NVARCHAR(255) DEFAULT ''`,
		`IF NOT EXISTS (SELECT 1 FROM syscolumns WHERE id=OBJECT_ID('Long_Breaks') AND name='EmailID')
			ALTER TABLE Long_Breaks ADD EmailID NVARCHAR(255) DEFAULT ''`,
	}
	for _, mig := range migrations {
		if _, err := m.db.Exec(mig); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	return nil
}

func (m *DatabaseManager) ExecuteQuery(query string, args ...any) (*QueryResult, error) {
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

	return &QueryResult{Columns: columns, Rows: result}, nil
}

func (m *DatabaseManager) ExecuteNonQuery(query string, args ...any) error {
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
