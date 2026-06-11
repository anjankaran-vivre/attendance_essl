package data

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"attendance_essl/database"
	"attendance_essl/messaging"
)

type SaveFunc func(errors []PunchError) (dupCount, breakCount int)

type outSession struct {
	outTime    time.Time
	device     string
	alerted10  bool
	alerted15  bool
	alerted65  bool
}

func cellString(v any) string {
	if t, ok := v.(time.Time); ok {
		return t.Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("%v", v)
}

type PunchWatcher struct {
	db          *database.MSSQLDatabase
	explorer    *Explorer
	logTable    string
	deviceTable string
	saveFn      SaveFunc
	cliq        *messaging.CliqConfig
	hrEmail      string
	adminEmails  []string
	testEmail    string
	interval    time.Duration
	lastPunch   time.Time
	mu          sync.Mutex
	nameLookup  func(userID string) string
	emailLookup func(userID string) string
	outSessions map[string]*outSession
}

func NewPunchWatcher(db *database.MSSQLDatabase, explorer *Explorer, logTable, deviceTable string, saveFn SaveFunc, cliq *messaging.CliqConfig, hrEmail string, adminEmails []string, nameLookup func(userID string) string, emailLookup func(userID string) string) *PunchWatcher {
	return &PunchWatcher{
		db:          db,
		explorer:    explorer,
		logTable:    logTable,
		deviceTable: deviceTable,
		saveFn:      saveFn,
		cliq:        cliq,
		hrEmail:     hrEmail,
		adminEmails: adminEmails,
		testEmail:   "anjan.karan@vivrepanels.com",
		interval:    30 * time.Second,
		nameLookup:  nameLookup,
		emailLookup: emailLookup,
		outSessions: make(map[string]*outSession),
	}
}

func (w *PunchWatcher) Start(ctx context.Context) {
	w.loadLastPunch()

	slog.Info("punch watcher started", "interval", w.interval)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("punch watcher stopped")
			return
		case <-ticker.C:
			w.check()
		}
	}
}

func (w *PunchWatcher) loadLastPunch() {
	res, err := w.db.ExecuteQuery(fmt.Sprintf("SELECT MAX(LogDate) FROM [%s]", w.logTable))
	if err != nil {
		slog.Warn("watcher: failed to load last punch time", "error", err)
		return
	}
	if len(res.Rows) > 0 && len(res.Rows[0]) > 0 && res.Rows[0][0] != nil {
		if t, ok := res.Rows[0][0].(time.Time); ok {
			w.lastPunch = t
			slog.Info("watcher: loaded last punch time", "time", t)
		}
	}
}

func (w *PunchWatcher) getLastPunchInfo(userID string) (direction string, punchTime time.Time, device string, err error) {
	query := fmt.Sprintf(
		`SELECT TOP 1 dl.Direction, dl.LogDate, d.DeviceSName
		 FROM [%s] dl
		 LEFT JOIN [%s] d ON dl.DeviceId = d.DeviceId
		 WHERE dl.UserId = @p1
		 ORDER BY dl.LogDate DESC`,
		w.logTable, w.deviceTable,
	)
	res, qErr := w.db.ExecuteQuery(query, userID)
	if qErr != nil {
		err = qErr
		return
	}
	if len(res.Rows) > 0 && len(res.Rows[0]) >= 2 {
		direction = fmt.Sprintf("%v", res.Rows[0][0])
		if t, ok := res.Rows[0][1].(time.Time); ok {
			punchTime = t
		}
		if len(res.Rows[0]) >= 3 {
			device = fmt.Sprintf("%v", res.Rows[0][2])
		}
	}
	return
}

func (w *PunchWatcher) check() {
	w.mu.Lock()
	defer w.mu.Unlock()

	affected, err := w.getAffectedUsers()
	if err != nil {
		slog.Error("watcher: failed to get affected users", "error", err)
		return
	}

	if len(affected) > 0 {
		slog.Info("watcher: new punches detected", "count", len(affected), "user_ids", affected)
	}

	for _, userID := range affected {
		employeeName := ""
		employeeEmail := ""
		if w.nameLookup != nil {
			employeeName = w.nameLookup(userID)
		}
		if w.emailLookup != nil {
			employeeEmail = w.emailLookup(userID)
		}

		dir, punchTime, punchDevice, err := w.getLastPunchInfo(userID)
		if err == nil {
			if dir == "IN" {
				delete(w.outSessions, userID)
			} else if dir == "OUT" {
				if _, exists := w.outSessions[userID]; !exists {
					w.outSessions[userID] = &outSession{
						outTime: punchTime,
						device:  punchDevice,
					}
					slog.Info("watcher: tracking OUT session", "user", userID, "since", punchTime)
				}
			}
		}

		result, err := w.explorer.DetectDuplicatePunchesForUser(w.logTable, w.deviceTable, userID)
		if err != nil {
			slog.Error("watcher: duplicate check failed", "user", userID, "error", err)
			continue
		}
		for _, row := range result.Rows {
			pe := PunchError{ErrorType: "Duplicate Punch", EmployeeName: employeeName, EmployeeEmail: employeeEmail}
			for i, col := range result.Columns {
				val := cellString(row[i])
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
			pe.Details = fmt.Sprintf("Duplicate %s Punch - User %s punched twice within %s mins",
				pe.Direction, pe.UserID, pe.MinutesGap)
			w.saveFn([]PunchError{pe})
			w.sendDuplicateAlerts(pe)
		}

		result, err = w.explorer.DetectLongBreaksForUser(w.logTable, w.deviceTable, 65, userID)
		if err != nil {
			slog.Error("watcher: long break check failed", "user", userID, "error", err)
			continue
		}
		for _, row := range result.Rows {
			pe := PunchError{ErrorType: "Long Break", EmployeeName: employeeName, EmployeeEmail: employeeEmail}
			for i, col := range result.Columns {
				val := cellString(row[i])
				switch col {
				case "UserId":
					pe.UserID = val
				case "outPunchTime":
					pe.PunchInTime = val
				case "device":
					pe.Device = val
				case "outDevice":
					pe.OutDevice = val
				case "outDirection":
					pe.Direction = val
				case "returnPunchTime":
					pe.PunchOutTime = val
				case "minutesGap":
					pe.MinutesGap = val
				}
			}
			pe.Details = fmt.Sprintf("Long Break - User %s was OUT for %s mins",
				pe.UserID, pe.MinutesGap)
			w.saveFn([]PunchError{pe})
			w.sendLongBreakAlerts(pe)
		}
	}

	// Progressive OUT alerts for active sessions
	for userID, session := range w.outSessions {
		employeeName := ""
		employeeEmail := ""
		if w.nameLookup != nil {
			employeeName = w.nameLookup(userID)
		}
		if w.emailLookup != nil {
			employeeEmail = w.emailLookup(userID)
		}

		minutes := int(time.Since(session.outTime).Minutes())

		if minutes >= 10 && !session.alerted10 {
			session.alerted10 = true
			w.sendOutReminder(employeeEmail, employeeName, userID, session, minutes)
		}
		if minutes >= 15 && !session.alerted15 {
			session.alerted15 = true
			w.sendOutReminder(employeeEmail, employeeName, userID, session, minutes)
		}
		if minutes >= 65 && !session.alerted65 {
			session.alerted65 = true
			w.sendOutAt65(employeeEmail, employeeName, userID, session, minutes)
		}
	}

	w.updateLastPunch()
}

func (w *PunchWatcher) sendDuplicateAlerts(pe PunchError) {
	if w.cliq == nil {
		return
	}

	label := "Missed Punching OUT"
	dir := "IN"
	if pe.Direction == "OUT" {
		label = "Missed Punching IN"
		dir = "OUT"
	}

	channelMsg := fmt.Sprintf("*%s*. *Employee:* %s (ID: %s). *Previous Punch %s:* %s at %s. *Current Punch %s:* %s at %s. *Gap:* %s minutes.",
		label, pe.EmployeeName, pe.UserID,
		dir, pe.PunchInTime, pe.Device,
		dir, pe.PunchOutTime, pe.SecondDevice, pe.MinutesGap)

	empMsg := fmt.Sprintf("Hi @%s, *%s*. You have a *%s* punch at %s on %s followed by another *%s* punch at %s on %s. *Gap:* %s minutes.",
		pe.EmployeeEmail, label, dir, pe.PunchInTime, pe.Device,
		dir, pe.PunchOutTime, pe.SecondDevice, pe.MinutesGap)

	if pe.EmployeeEmail != "" {
		if err := w.cliq.SendDM(pe.EmployeeEmail, empMsg); err != nil {
			slog.Error("watcher: cliq employee dm failed", "email", pe.EmployeeEmail, "error", err)
		} else {
			slog.Info("watcher: sent duplicate alert to employee", "user", pe.UserID, "email", pe.EmployeeEmail)
		}
	}

	if w.hrEmail != "" {
		if err := w.cliq.SendDM(w.hrEmail, channelMsg); err != nil {
			slog.Error("watcher: cliq hr dm failed", "email", w.hrEmail, "error", err)
		} else {
			slog.Info("watcher: sent duplicate alert to hr", "hr_email", w.hrEmail)
		}
	}
	for _, email := range w.adminEmails {
		if err := w.cliq.SendDM(email, channelMsg); err != nil {
			slog.Error("watcher: cliq admin dm failed", "email", email, "error", err)
		} else {
			slog.Info("watcher: sent duplicate alert to admin", "admin_email", email)
		}
	}
}

func (w *PunchWatcher) sendLongBreakAlerts(pe PunchError) {
	if w.cliq == nil {
		return
	}

	channelMsg := fmt.Sprintf("*Break Time Exceeded*. *Employee:* %s (ID: %s). *OUT:* %s at %s. *Return:* %s at %s. *Duration:* %s minutes (exceeded limit).",
		pe.EmployeeName, pe.UserID, pe.PunchInTime, pe.OutDevice, pe.PunchOutTime, pe.Device, pe.MinutesGap)

	empMsg := fmt.Sprintf("Hi @%s, *Break Time Exceeded*. You were OUT from %s at %s to %s at %s for %s minutes.",
		pe.EmployeeEmail, pe.PunchInTime, pe.OutDevice, pe.PunchOutTime, pe.Device, pe.MinutesGap)

	if pe.EmployeeEmail != "" {
		if err := w.cliq.SendDM(pe.EmployeeEmail, empMsg); err != nil {
			slog.Error("watcher: cliq employee dm failed", "email", pe.EmployeeEmail, "error", err)
		} else {
			slog.Info("watcher: sent break alert to employee", "user", pe.UserID, "email", pe.EmployeeEmail)
		}
	}

	if w.hrEmail != "" {
		if err := w.cliq.SendDM(w.hrEmail, channelMsg); err != nil {
			slog.Error("watcher: cliq hr dm failed", "email", w.hrEmail, "error", err)
		} else {
			slog.Info("watcher: sent break alert to hr", "hr_email", w.hrEmail)
		}
	}
	for _, email := range w.adminEmails {
		if err := w.cliq.SendDM(email, channelMsg); err != nil {
			slog.Error("watcher: cliq admin dm failed", "email", email, "error", err)
		} else {
			slog.Info("watcher: sent break alert to admin", "admin_email", email)
		}
	}
}

func (w *PunchWatcher) sendOutReminder(employeeEmail, employeeName, userID string, session *outSession, minutes int) {
	if w.cliq == nil || employeeEmail == "" {
		return
	}

	outTime := session.outTime.Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf("Hi @%s, *Reminder*. You have been OUT since %s on %s (%d minutes).",
		employeeEmail, outTime, session.device, minutes)

	if err := w.cliq.SendDM(employeeEmail, msg); err != nil {
		slog.Error("watcher: cliq out reminder dm failed", "email", employeeEmail, "error", err)
	} else {
		slog.Info("watcher: sent %d min reminder", "user", userID, "email", employeeEmail, "minutes", minutes)
	}

	w.saveFn([]PunchError{{
		ErrorType:     "Out Reminder",
		UserID:        userID,
		EmployeeName:  employeeName,
		EmployeeEmail: employeeEmail,
		PunchInTime:   outTime,
		Device:        session.device,
		MinutesGap:    fmt.Sprintf("%d", minutes),
		Details:       msg,
	}})
}

func (w *PunchWatcher) sendOutAt65(employeeEmail, employeeName, userID string, session *outSession, minutes int) {
	if w.cliq == nil || employeeEmail == "" {
		return
	}

	outTime := session.outTime.Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf("Hi @%s, *Break Time Exceeded*. You have been OUT since %s on %s (%d minutes).",
		employeeEmail, outTime, session.device, minutes)

	if err := w.cliq.SendDM(employeeEmail, msg); err != nil {
		slog.Error("watcher: cliq 65min dm failed", "email", employeeEmail, "error", err)
	} else {
		slog.Info("watcher: sent 65 min alert", "user", userID, "email", employeeEmail)
	}

	w.saveFn([]PunchError{{
		ErrorType:     "Out Alert",
		UserID:        userID,
		EmployeeName:  employeeName,
		EmployeeEmail: employeeEmail,
		PunchInTime:   outTime,
		Device:        session.device,
		MinutesGap:    fmt.Sprintf("%d", minutes),
		Details:       msg,
	}})
}

func (w *PunchWatcher) getAffectedUsers() ([]string, error) {
	query := fmt.Sprintf(
		`SELECT DISTINCT UserId FROM [%s] WHERE LogDate > @p1`,
		w.logTable,
	)
	res, err := w.db.ExecuteQuery(query, w.lastPunch)
	if err != nil {
		return nil, err
	}

	var users []string
	for _, row := range res.Rows {
		if len(row) > 0 {
			users = append(users, fmt.Sprintf("%v", row[0]))
		}
	}
	return users, nil
}

func (w *PunchWatcher) updateLastPunch() {
	res, err := w.db.ExecuteQuery(fmt.Sprintf("SELECT MAX(LogDate) FROM [%s]", w.logTable))
	if err != nil {
		slog.Warn("watcher: failed to update last punch time", "error", err)
		return
	}
	if len(res.Rows) > 0 && len(res.Rows[0]) > 0 && res.Rows[0][0] != nil {
		if t, ok := res.Rows[0][0].(time.Time); ok {
			w.lastPunch = t
		}
	}
}
