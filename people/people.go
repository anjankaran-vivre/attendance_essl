package people

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"attendance_essl/data"
	"attendance_essl/database"

	_ "modernc.org/sqlite"
)

type EmployeeDetail struct {
	ZohoID              string `json:"zoho_id"`
	FirstName           string `json:"first_name"`
	LastName            string `json:"last_name"`
	EmailID             string `json:"email_id"`
	Mobile              string `json:"mobile"`
	Designation         string `json:"designation"`
	Department          string `json:"department"`
	ReportingToID       string `json:"reporting_to_id"`
	ReportingTo         string `json:"reporting_to"`
	SecondReportingToID string `json:"second_reporting_to_id"`
	SecondReportingTo   string `json:"second_reporting_to"`
	Photo               string `json:"photo"`
	EmployeeID          string `json:"employee_id"`
	EmployeeStatus      string `json:"employee_status"`
	Team                string `json:"team"`
	LocationName        string `json:"location_name"`
}

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) db() *database.DatabaseManager {
	return database.GetEMP()
}

func (m *Manager) CreateTables() error {
	return m.db().InitTables()
}

func (m *Manager) SyncFromZoho(rawJSON []byte) (int, error) {
	var resp zohoPeopleResponse
	if err := json.Unmarshal(rawJSON, &resp); err != nil {
		return 0, fmt.Errorf("failed to parse Zoho JSON: %w", err)
	}

	inserted := 0
	for _, resultMap := range resp.Response.Result {
		for recordID, employees := range resultMap {
			for _, emp := range employees {
				emp.ZohoID = parseID(recordID)
				if err := m.upsertEmployee(emp); err != nil {
					slog.Error("failed to upsert employee", "zoho_id", recordID, "error", err)
					continue
				}
				inserted++
			}
		}
	}

	return inserted, nil
}

func (m *Manager) SyncFromSQLite(dbPath string) (int, error) {
	if dbPath == "" {
		dbPath = "orgchart.db"
	}

	if _, err := os.Stat(dbPath); err != nil {
		return 0, fmt.Errorf("sqlite db not found at %s: %w", dbPath, err)
	}

	sqldb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open sqlite db: %w", err)
	}
	defer sqldb.Close()

	rows, err := sqldb.Query(`SELECT zoho_id, first_name, last_name, email_id, mobile,
		designation, department, employee_id, employee_status, team, location_name,
		reporting_to, photo
		FROM employees`)
	if err != nil {
		return 0, fmt.Errorf("sqlite query failed: %w", err)
	}
	defer rows.Close()

	inserted := 0
	for rows.Next() {
		var emp sqliteEmployee
		if err := rows.Scan(&emp.ZohoID, &emp.FirstName, &emp.LastName, &emp.EmailID,
			&emp.Mobile, &emp.Designation, &emp.Department, &emp.EmployeeID,
			&emp.EmployeeStatus, &emp.Team, &emp.LocationName, &emp.ReportingTo,
			&emp.Photo); err != nil {
			slog.Error("failed to scan sqlite row", "error", err)
			continue
		}

		if err := m.upsertFromSQLite(emp); err != nil {
			slog.Error("failed to upsert from sqlite", "zoho_id", emp.ZohoID, "error", err)
			continue
		}
		inserted++
	}

	if err := rows.Err(); err != nil {
		return inserted, fmt.Errorf("rows iteration error: %w", err)
	}

	return inserted, nil
}

type sqliteEmployee struct {
	ZohoID          string
	FirstName       string
	LastName        string
	EmailID         string
	Mobile          string
	Designation     string
	Department      string
	EmployeeID      string
	EmployeeStatus  string
	Team            string
	LocationName    string
	ReportingTo     string
	Photo           string
}

func (m *Manager) upsertFromSQLite(emp sqliteEmployee) error {
	query := `
		IF EXISTS (SELECT 1 FROM EmployeeDetails WHERE ZohoID = @p1)
			UPDATE EmployeeDetails
			SET FirstName = @p2, LastName = @p3, EmailID = @p4, EmployeeID = @p5,
			    EmployeeStatus = @p6, Mobile = @p7, Designation = @p8, Department = @p9,
			    Team = @p10, LocationName = @p11, ReportingTo = @p12, Photo = @p13,
			    UpdatedAt = GETDATE()
			WHERE ZohoID = @p1
		ELSE
			INSERT INTO EmployeeDetails (ZohoID, FirstName, LastName, EmailID, EmployeeID,
			    EmployeeStatus, Mobile, Designation, Department, Team, LocationName,
			    ReportingTo, Photo)
			VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12, @p13)
	`
	return m.db().ExecuteNonQuery(query,
		emp.ZohoID, emp.FirstName, emp.LastName, emp.EmailID, emp.EmployeeID,
		emp.EmployeeStatus, emp.Mobile, emp.Designation, emp.Department,
		emp.Team, emp.LocationName, emp.ReportingTo, emp.Photo,
	)
}

func (m *Manager) upsertEmployee(emp zohoEmployeeRecord) error {
	zohoID := fmt.Sprintf("%d", emp.ZohoID)
	query := `
		IF EXISTS (SELECT 1 FROM EmployeeDetails WHERE ZohoID = @p1)
			UPDATE EmployeeDetails
			SET FirstName = @p2, LastName = @p3, EmailID = @p4, EmployeeID = @p5,
			    EmployeeStatus = @p6, Mobile = @p7, Designation = @p8, Department = @p9,
			    Team = @p10, LocationName = @p11, ReportingTo = @p12, UpdatedAt = GETDATE()
			WHERE ZohoID = @p1
		ELSE
			INSERT INTO EmployeeDetails (ZohoID, FirstName, LastName, EmailID, EmployeeID,
			    EmployeeStatus, Mobile, Designation, Department, Team, LocationName, ReportingTo)
			VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12)
	`
	return m.db().ExecuteNonQuery(query,
		zohoID, emp.FirstName, emp.LastName, emp.EmailID, emp.EmployeeID,
		emp.Employeestatus, emp.Mobile, emp.Designation, emp.Department,
		emp.Team, emp.LocationName, emp.ReportingTo,
	)
}

func (m *Manager) UpsertEmployee(emp EmployeeDetail) error {
	query := `
		IF EXISTS (SELECT 1 FROM EmployeeDetails WHERE ZohoID = @p1)
			UPDATE EmployeeDetails
			SET FirstName = @p2, LastName = @p3, EmailID = @p4, Mobile = @p5,
			    Designation = @p6, Department = @p7, ReportingToID = @p8,
			    ReportingTo = @p9, SecondReportingToID = @p10, SecondReportingTo = @p11,
			    Photo = @p12, EmployeeID = @p13, EmployeeStatus = @p14, Team = @p15,
			    LocationName = @p16, UpdatedAt = GETDATE()
			WHERE ZohoID = @p1
		ELSE
			INSERT INTO EmployeeDetails (ZohoID, FirstName, LastName, EmailID, Mobile,
			    Designation, Department, ReportingToID, ReportingTo,
			    SecondReportingToID, SecondReportingTo, Photo, EmployeeID,
			    EmployeeStatus, Team, LocationName)
			VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10, @p11, @p12,
			    @p13, @p14, @p15, @p16)
	`
	return m.db().ExecuteNonQuery(query,
		emp.ZohoID, emp.FirstName, emp.LastName, emp.EmailID, emp.Mobile,
		emp.Designation, emp.Department, emp.ReportingToID, emp.ReportingTo,
		emp.SecondReportingToID, emp.SecondReportingTo, emp.Photo, emp.EmployeeID,
		emp.EmployeeStatus, emp.Team, emp.LocationName,
	)
}

func (m *Manager) GetAll() ([]EmployeeDetail, error) {
	res, err := m.db().ExecuteQuery(`SELECT ZohoID, FirstName, LastName, EmailID, Mobile,
		Designation, Department, EmployeeID, EmployeeStatus, Team, LocationName
		FROM EmployeeDetails ORDER BY FirstName, LastName`)
	if err != nil {
		return nil, err
	}

	var people []EmployeeDetail
	for _, row := range res.Rows {
		p := EmployeeDetail{}
		if len(row) > 0 {
			p.ZohoID = fmt.Sprintf("%v", row[0])
		}
		if len(row) > 1 {
			p.FirstName = fmt.Sprintf("%v", row[1])
		}
		if len(row) > 2 {
			p.LastName = fmt.Sprintf("%v", row[2])
		}
		if len(row) > 3 {
			p.EmailID = fmt.Sprintf("%v", row[3])
		}
		if len(row) > 4 {
			p.Mobile = fmt.Sprintf("%v", row[4])
		}
		if len(row) > 5 {
			p.Designation = fmt.Sprintf("%v", row[5])
		}
		if len(row) > 6 {
			p.Department = fmt.Sprintf("%v", row[6])
		}
		if len(row) > 7 {
			p.EmployeeID = fmt.Sprintf("%v", row[7])
		}
		if len(row) > 8 {
			p.EmployeeStatus = fmt.Sprintf("%v", row[8])
		}
		if len(row) > 9 {
			p.Team = fmt.Sprintf("%v", row[9])
		}
		if len(row) > 10 {
			p.LocationName = fmt.Sprintf("%v", row[10])
		}
		people = append(people, p)
	}
	return people, nil
}

func (m *Manager) GetEmailByEmployeeID(employeeID string) (string, error) {
	res, err := m.db().ExecuteQuery(`SELECT EmailID FROM EmployeeDetails WHERE EmployeeID = @p1`, employeeID)
	if err != nil {
		return "", err
	}
	if len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
		return fmt.Sprintf("%v", res.Rows[0][0]), nil
	}
	return "", nil
}

func (m *Manager) GetNameByEmployeeID(employeeID string) string {
	res, err := m.db().ExecuteQuery(`SELECT FirstName, LastName FROM EmployeeDetails WHERE EmployeeID = @p1`, employeeID)
	if err != nil {
		return ""
	}
	if len(res.Rows) > 0 && len(res.Rows[0]) >= 2 {
		first := fmt.Sprintf("%v", res.Rows[0][0])
		last := fmt.Sprintf("%v", res.Rows[0][1])
		if last != "" {
			return first + " " + last
		}
		return first
	}
	return ""
}

func (m *Manager) SaveDetectedErrors(errors []data.PunchError) (dupCount, breakCount int) {
	for _, e := range errors {
		email, _ := m.GetEmailByEmployeeID(e.UserID)

		if e.ErrorType == "Duplicate Punch" {
			q := `INSERT INTO Duplicate_Punches (UserID, EmailID, FirstPunchTime, FirstDevice,
				FirstDirection, SecondPunchTime, SecondDevice, SecondDirection, MinutesGap, Details)
				VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9, @p10)`
			if err := m.db().ExecuteNonQuery(q,
				e.UserID, email, e.PunchInTime, e.Device, e.Direction,
				e.PunchOutTime, e.SecondDevice, e.SecondDirection, e.MinutesGap, e.Details,
			); err != nil {
				slog.Error("failed to save duplicate punch", "user", e.UserID, "error", err)
				continue
			}
			dupCount++
		} else if e.ErrorType == "Long Break" {
			q := `INSERT INTO Long_Breaks (UserID, EmailID, OutPunchTime, OutDirection,
				ReturnPunchTime, Device, MinutesGap, Details)
				VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8)`
			if err := m.db().ExecuteNonQuery(q,
				e.UserID, email, e.PunchInTime, e.Direction,
				e.PunchOutTime, e.Device, e.MinutesGap, e.Details,
			); err != nil {
				slog.Error("failed to save long break", "user", e.UserID, "error", err)
				continue
			}
			breakCount++
		} else if e.ErrorType == "Out Reminder" || e.ErrorType == "Out Alert" {
			q := `INSERT INTO Out_Reminders (UserID, EmailID, EmployeeName, OutTime, Device, Minutes, AlertType, Details)
				VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8)`
			if err := m.db().ExecuteNonQuery(q,
				e.UserID, email, e.EmployeeName, e.PunchInTime, e.Device, e.MinutesGap, e.ErrorType, e.Details,
			); err != nil {
				slog.Error("failed to save out reminder", "user", e.UserID, "error", err)
				continue
			}
		}
	}
	return
}

func (m *Manager) SaveDuplicatePunches(errors []data.PunchError) (int, error) {
	saved := 0
	for _, e := range errors {
		if e.ErrorType != "Duplicate Punch" {
			continue
		}
		query := `INSERT INTO Duplicate_Punches (UserID, FirstPunchTime, FirstDevice, FirstDirection,
			SecondPunchTime, SecondDevice, SecondDirection, MinutesGap, Details)
			VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7, @p8, @p9)`
		if err := m.db().ExecuteNonQuery(query,
			e.UserID, e.PunchInTime, e.Device, e.Direction,
			e.PunchOutTime, e.SecondDevice, e.SecondDirection, e.MinutesGap, e.Details,
		); err != nil {
			slog.Error("failed to save duplicate punch", "error", err)
			continue
		}
		saved++
	}
	return saved, nil
}

func (m *Manager) SaveLongBreaks(errors []data.PunchError) (int, error) {
	saved := 0
	for _, e := range errors {
		if e.ErrorType != "Long Break" {
			continue
		}
		query := `INSERT INTO Long_Breaks (UserID, OutPunchTime, OutDirection,
			ReturnPunchTime, Device, MinutesGap, Details)
			VALUES (@p1, @p2, @p3, @p4, @p5, @p6, @p7)`
		if err := m.db().ExecuteNonQuery(query,
			e.UserID, e.PunchInTime, e.Direction,
			e.PunchOutTime, e.Device, e.MinutesGap, e.Details,
		); err != nil {
			slog.Error("failed to save long break", "error", err)
			continue
		}
		saved++
	}
	return saved, nil
}

type zohoEmployeeRecord struct {
	EmailID        string `json:"EmailID"`
	EmployeeID     string `json:"EmployeeID"`
	FirstName      string `json:"FirstName"`
	LastName       string `json:"LastName"`
	Employeestatus string `json:"Employeestatus"`
	ZohoID         int64  `json:"Zoho_ID"`
	Mobile         string `json:"Mobile"`
	Designation    string `json:"Designation"`
	Department     string `json:"Department"`
	Team           string `json:"Team"`
	LocationName   string `json:"LocationName"`
	ReportingTo    string `json:"Reporting_To"`
}

type zohoPeopleResponse struct {
	Response struct {
		Result []map[string][]zohoEmployeeRecord `json:"result"`
		Message string                           `json:"message"`
		Status  int                              `json:"status"`
	} `json:"response"`
}

func parseID(s string) int64 {
	var id int64
	fmt.Sscanf(s, "%d", &id)
	return id
}
