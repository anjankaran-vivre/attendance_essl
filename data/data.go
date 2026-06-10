package data

import (
	"fmt"
	"log/slog"

	"attendance_essl/database"
)

type ColumnInfo struct {
	Name      string `json:"name"`
	DataType  string `json:"data_type"`
	MaxLength string `json:"max_length,omitempty"`
	Nullable  string `json:"nullable,omitempty"`
}

type PreviewResult struct {
	TableName string          `json:"table_name"`
	Columns   []ColumnInfo    `json:"columns"`
	TotalRows int             `json:"total_rows,omitempty"`
	Sample    [][]any         `json:"sample"`
}

type Explorer struct {
	db *database.MSSQLDatabase
}

func NewExplorer(db *database.MSSQLDatabase) *Explorer {
	return &Explorer{db: db}
}

func (e *Explorer) GetSchema(tableName string) ([]ColumnInfo, error) {
	query := `
		SELECT
			COLUMN_NAME,
			DATA_TYPE,
			ISNULL(CAST(CHARACTER_MAXIMUM_LENGTH AS VARCHAR), '') AS MAX_LENGTH,
			IS_NULLABLE
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_NAME = @p1
		ORDER BY ORDINAL_POSITION
	`
	res, err := e.db.ExecuteQuery(query, tableName)
	if err != nil {
		return nil, fmt.Errorf("schema query failed: %w", err)
	}

	var columns []ColumnInfo
	for _, row := range res.Rows {
		col := ColumnInfo{}
		if len(row) > 0 {
			col.Name = fmt.Sprintf("%v", row[0])
		}
		if len(row) > 1 {
			col.DataType = fmt.Sprintf("%v", row[1])
		}
		if len(row) > 2 {
			col.MaxLength = fmt.Sprintf("%v", row[2])
		}
		if len(row) > 3 {
			col.Nullable = fmt.Sprintf("%v", row[3])
		}
		columns = append(columns, col)
	}
	return columns, nil
}

func (e *Explorer) GetSampleData(tableName string, limit int) (*PreviewResult, error) {
	// Get schema
	columns, err := e.GetSchema(tableName)
	if err != nil {
		return nil, err
	}

	// Get total row count
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)
	countRes, err := e.db.ExecuteQuery(countQuery)
	totalRows := 0
	if err == nil && len(countRes.Rows) > 0 && len(countRes.Rows[0]) > 0 {
		fmt.Sscanf(fmt.Sprintf("%v", countRes.Rows[0][0]), "%d", &totalRows)
	}

	// Get column names for SELECT
	var colNames string
	for i, c := range columns {
		if i > 0 {
			colNames += ", "
		}
		colNames += fmt.Sprintf("[%s]", c.Name)
	}

	// Get sample data
	sampleQuery := fmt.Sprintf("SELECT TOP %d %s FROM %s", limit, colNames, tableName)
	slog.Info("fetching sample data", "table", tableName, "limit", limit)
	res, err := e.db.ExecuteQuery(sampleQuery)
	if err != nil {
		return nil, fmt.Errorf("sample query failed: %w", err)
	}

	return &PreviewResult{
		TableName: tableName,
		Columns:   columns,
		TotalRows: totalRows,
		Sample:    res.Rows,
	}, nil
}

func (e *Explorer) GetDistinctValues(tableName, column string) ([]string, error) {
	query := fmt.Sprintf("SELECT DISTINCT [%s] FROM %s ORDER BY [%s]", column, tableName, column)
	res, err := e.db.ExecuteQuery(query)
	if err != nil {
		return nil, err
	}

	var values []string
	for _, row := range res.Rows {
		if len(row) > 0 {
			values = append(values, fmt.Sprintf("%v", row[0]))
		}
	}
	return values, nil
}

type PunchError struct {
	UserID          string `json:"User_Id"`
	EmployeeName    string `json:"Employee_Name"`
	EmployeeEmail   string `json:"Employee_Email"`
	PunchInTime     string `json:"punch_in_time"`
	PunchOutTime    string `json:"punch_out_time"`
	ErrorType       string `json:"Error_Type"`
	Device          string `json:"Device"`
	SecondDevice    string `json:"Second_Device"`
	MinutesGap      string `json:"Minutes_Gap"`
	Direction       string `json:"Direction"`
	SecondDirection string `json:"Second_Direction"`
	Details         string `json:"Details"`
}

func (e *Explorer) GetAllPunchErrors(logTable, deviceTable string, limit int, breakThreshold int) ([]PunchError, error) {
	var errors []PunchError

	dupResult, err := e.DetectDuplicatePunches(logTable, deviceTable, limit)
	if err != nil {
		return nil, fmt.Errorf("duplicate detection failed: %w", err)
	}
	for _, row := range dupResult.Rows {
		pe := PunchError{ErrorType: "Duplicate Punch"}
		for i, col := range dupResult.Columns {
			val := fmt.Sprintf("%v", row[i])
			switch col {
			case "UserId":
				pe.UserID = val
			case "firstPunch":
				pe.PunchInTime = val
			case "firstDevice":
				pe.Device = val
			case "firstDirection":
				pe.Direction = val
			case "secondPunch":
				pe.PunchOutTime = val
			case "secondDevice":
				pe.SecondDevice = val
			case "secondDirection":
				pe.SecondDirection = val
			case "minutesBetween":
				pe.MinutesGap = val
			}
		}
		pe.Details = fmt.Sprintf("Duplicate %s Punch - User %s punched twice within %s mins on %s",
			pe.Direction, pe.UserID, pe.MinutesGap, pe.Device)
		errors = append(errors, pe)
	}

	breakResult, err := e.DetectLongBreaks(logTable, deviceTable, breakThreshold, limit)
	if err != nil {
		return nil, fmt.Errorf("long break detection failed: %w", err)
	}
	for _, row := range breakResult.Rows {
		pe := PunchError{ErrorType: "Long Break"}
		for i, col := range breakResult.Columns {
			val := fmt.Sprintf("%v", row[i])
			switch col {
			case "UserId":
				pe.UserID = val
			case "outPunchTime":
				pe.PunchInTime = val
			case "device":
				pe.Device = val
			case "outDirection":
				pe.Direction = val
			case "returnPunchTime":
				pe.PunchOutTime = val
			case "minutesGap":
				pe.MinutesGap = val
			}
		}
		pe.Details = fmt.Sprintf("Long Break - User %s was OUT for %s mins from %s to %s on %s",
			pe.UserID, pe.MinutesGap, pe.PunchInTime, pe.PunchOutTime, pe.Device)
		errors = append(errors, pe)
	}

	return errors, nil
}

func (e *Explorer) DetectDuplicatePunchesForUser(logTable, deviceTable, userID string) (*database.QueryResult, error) {
	query := fmt.Sprintf(`
WITH Ranked AS (
    SELECT
        dl.UserId, dl.DeviceId, dl.LogDate, dl.Direction,
        d.DeviceFName, d.DeviceSName,
        ROW_NUMBER() OVER (ORDER BY dl.LogDate DESC) AS rn
    FROM [%s] dl
    LEFT JOIN [%s] d ON dl.DeviceId = d.DeviceId
    WHERE dl.UserId = @p1
)
SELECT
    r2.UserId,
    r2.LogDate AS firstPunch,
    r2.DeviceFName AS firstFloor,
    r2.DeviceSName AS firstDevice,
    r2.DeviceId AS firstDeviceId,
    r2.Direction AS firstDirection,
    r1.LogDate AS secondPunch,
    r1.DeviceFName AS secondFloor,
    r1.DeviceSName AS secondDevice,
    r1.DeviceId AS secondDeviceId,
    r1.Direction AS secondDirection,
    DATEDIFF(SECOND, r2.LogDate, r1.LogDate) / 60 AS minutesBetween
FROM Ranked r1
JOIN Ranked r2 ON r2.rn = 2
WHERE r1.rn = 1 AND r1.Direction = r2.Direction
`, logTable, deviceTable)

	return e.db.ExecuteQuery(query, userID)
}

func (e *Explorer) DetectLongBreaksForUser(logTable, deviceTable string, minutesThreshold int, userID string) (*database.QueryResult, error) {
	query := fmt.Sprintf(`
WITH Ranked AS (
    SELECT
        dl.UserId, dl.DeviceId, dl.LogDate, dl.Direction,
        d.DeviceFName, d.DeviceSName,
        ROW_NUMBER() OVER (ORDER BY dl.LogDate DESC) AS rn
    FROM [%s] dl
    LEFT JOIN [%s] d ON dl.DeviceId = d.DeviceId
    WHERE dl.UserId = @p1
)
SELECT
    r2.UserId,
    r2.LogDate AS outPunchTime,
    r2.Direction AS outDirection,
    r1.LogDate AS returnPunchTime,
    r1.Direction AS returnDirection,
    r1.DeviceFName AS floor,
    r1.DeviceSName AS device,
    r1.DeviceId,
    DATEDIFF(MINUTE, r2.LogDate, r1.LogDate) AS minutesGap
FROM Ranked r1
JOIN Ranked r2 ON r2.rn = 2
WHERE r1.rn = 1 AND r2.Direction = 'OUT' AND r1.Direction = 'IN'
  AND DATEDIFF(MINUTE, r2.LogDate, r1.LogDate) > %d
  AND CAST(r2.LogDate AS TIME) >= '10:15'
  AND CAST(r2.LogDate AS TIME) <= '17:59'
  AND CAST(r2.LogDate AS DATE) = CAST(r1.LogDate AS DATE)
`, logTable, deviceTable, minutesThreshold)

	return e.db.ExecuteQuery(query, userID)
}

func (e *Explorer) DetectDuplicatePunches(logTable, deviceTable string, limit int) (*database.QueryResult, error) {
	query := fmt.Sprintf(`
WITH Ranked AS (
    SELECT
        dl.UserId,
        dl.DeviceId,
        dl.LogDate,
        dl.Direction,
        d.DeviceFName,
        d.DeviceSName,
        ROW_NUMBER() OVER (
            PARTITION BY dl.UserId
            ORDER BY dl.LogDate DESC
        ) AS rn
    FROM [%s] dl
    LEFT JOIN [%s] d ON dl.DeviceId = d.DeviceId
)
SELECT TOP %d
    r2.UserId,
    r2.LogDate AS firstPunch,
    r2.DeviceFName AS firstFloor,
    r2.DeviceSName AS firstDevice,
    r2.DeviceId AS firstDeviceId,
    r2.Direction AS firstDirection,
    r1.LogDate AS secondPunch,
    r1.DeviceFName AS secondFloor,
    r1.DeviceSName AS secondDevice,
    r1.DeviceId AS secondDeviceId,
    r1.Direction AS secondDirection,
    DATEDIFF(SECOND, r2.LogDate, r1.LogDate) / 60 AS minutesBetween
FROM Ranked r1
JOIN Ranked r2 ON r1.UserId = r2.UserId AND r2.rn = 2
WHERE r1.rn = 1 AND r1.Direction = r2.Direction
ORDER BY r1.LogDate DESC
`, logTable, deviceTable, limit)

	slog.Info("checking last 2 punches for duplicates",
		"table", logTable,
	)
	return e.db.ExecuteQuery(query)
}

func (e *Explorer) DetectLongBreaks(logTable, deviceTable string, minutesThreshold int, limit int) (*database.QueryResult, error) {
	query := fmt.Sprintf(`
WITH Ranked AS (
    SELECT
        dl.UserId,
        dl.DeviceId,
        dl.LogDate,
        dl.Direction,
        d.DeviceFName,
        d.DeviceSName,
        ROW_NUMBER() OVER (
            PARTITION BY dl.UserId
            ORDER BY dl.LogDate DESC
        ) AS rn
    FROM [%s] dl
    LEFT JOIN [%s] d ON dl.DeviceId = d.DeviceId
)
SELECT TOP %d
    r2.UserId,
    r2.LogDate AS outPunchTime,
    r2.Direction AS outDirection,
    r1.LogDate AS returnPunchTime,
    r1.Direction AS returnDirection,
    r1.DeviceFName AS floor,
    r1.DeviceSName AS device,
    r1.DeviceId,
    DATEDIFF(MINUTE, r2.LogDate, r1.LogDate) AS minutesGap
FROM Ranked r1
JOIN Ranked r2 ON r1.UserId = r2.UserId AND r2.rn = 2
WHERE r1.rn = 1 AND r2.Direction = 'OUT' AND r1.Direction = 'IN'
  AND DATEDIFF(MINUTE, r2.LogDate, r1.LogDate) > %d
  AND CAST(r2.LogDate AS TIME) >= '10:15'
  AND CAST(r2.LogDate AS TIME) <= '17:59'
  AND CAST(r2.LogDate AS DATE) = CAST(r1.LogDate AS DATE)
ORDER BY r1.LogDate DESC
`, logTable, deviceTable, limit, minutesThreshold)

	slog.Info("checking last 2 punches for long breaks",
		"table", logTable,
		"threshold_minutes", minutesThreshold,
	)
	return e.db.ExecuteQuery(query)
}
