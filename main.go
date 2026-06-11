package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"attendance_essl/config"
	"attendance_essl/data"
	"attendance_essl/database"
	"attendance_essl/handlers"
	"attendance_essl/messaging"
	"attendance_essl/people"
)

func init() {
	gin.SetMode(gin.ReleaseMode)
}

var (
	db         *database.MSSQLDatabase
	explorer   *data.Explorer
	empManager *people.Manager
)

const tableName = "DeviceLogs_6_2026"

func getDB() error {
	if db == nil || !db.TestConnection() {
		connStr := config.GetMSSQL().ConnectionString()
		slog.Info("connecting to database", "server", config.GetMSSQL().Host, "database", config.GetMSSQL().Database)
		db = database.NewMSSQLDatabase(connStr)
		if err := db.Connect(); err != nil {
			db = nil
			return fmt.Errorf("database connection failed: %w", err)
		}
		slog.Info("database connected successfully")
		explorer = nil
	}
	if explorer == nil {
		explorer = data.NewExplorer(db)
	}
	return nil
}

func main() {
	slog.Info("starting application")
	slog.Info("connection config",
		"host", config.GetMSSQL().Host,
		"port", config.GetMSSQL().Port,
		"database", config.GetMSSQL().Database,
		"username", config.GetMSSQL().Username,
		"conn_string", config.DebugConnString(),
	)

	cliq := config.GetCliq()
	if cliq.Token != "" {
		messaging.Init(cliq.Token, cliq.HookID)
		slog.Info("cliq bot initialized", "hook_id", cliq.HookID)
	} else {
		slog.Warn("cliq not configured — set CLIQ_WEBHOOK_TOKEN and CLIQ_HOOK_ID in .env")
	}

	empCfg := config.GetEMP()
	if err := database.InitEMP(database.DBConfig{
		DBHost:     empCfg.Host,
		DBServer:   empCfg.Server,
		DBName:     empCfg.Database,
		DBUsername: empCfg.Username,
		DBPassword: empCfg.Password,
	}); err != nil {
		slog.Error("employee database init failed", "error", err)
	} else {
		slog.Info("employee database initialized",
			"host", empCfg.Host,
			"instance", empCfg.Server,
			"database", empCfg.Database,
		)
	}
	empManager = people.NewManager()

	r := gin.Default()

	r.GET("/", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"message": "Attendance ESSL API is running",
			"table":   tableName,
		})
	})

	r.GET("/health", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "database": "disconnected", "error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"status": "ok", "database": "connected"})
	})

	r.GET("/test/cliq", func(ctx *gin.Context) {
		if messaging.DefaultCliq == nil {
			ctx.JSON(http.StatusOK, gin.H{"status": "error", "message": "Cliq not configured"})
			return
		}
		channel := ctx.DefaultQuery("channel", "")
		email := ctx.DefaultQuery("email", "")
		text := "Test Message - Cliq bot is working correctly!"

		results := gin.H{}
		hasError := false

		if email != "" {
			if err := messaging.DefaultCliq.SendDM(email, text); err != nil {
				results["dm"] = "error: " + err.Error()
				hasError = true
			} else {
				results["dm"] = "sent to " + email
			}
		}

		if channel != "" || email == "" {
			if channel == "" {
				channel = "general"
			}
			if err := messaging.DefaultCliq.SendToChannel(channel, text); err != nil {
				results["channel"] = "error: " + err.Error()
				hasError = true
			} else {
				results["channel"] = "sent to " + channel
			}
		}

		if hasError {
			results["status"] = "partial"
		} else {
			results["status"] = "ok"
		}
		ctx.JSON(http.StatusOK, results)
	})

	r.Any("/test/cliq-msg", func(ctx *gin.Context) {
		if messaging.DefaultCliq == nil {
			ctx.JSON(http.StatusOK, gin.H{"status": "error", "message": "Cliq not configured"})
			return
		}

		channel := ctx.DefaultQuery("channel", "")
		email := ctx.DefaultQuery("email", "anjan.karan@vivrepanels.com")
		msg := "*Duplicate IN Punch Detected*. *Employee ID:* 197. *First IN:* 2026-06-09 15:30:00 at Main-Gate. *Second IN:* 2026-06-09 15:32:00 at Side-Gate. *Gap:* 2 minutes."

		results := gin.H{"text": msg}

		if err := messaging.DefaultCliq.SendToChannel(channel, msg); err != nil {
			results["channel_error"] = err.Error()
		} else {
			results["channel"] = "sent to " + channel
		}

		if email != "" {
			if err := messaging.DefaultCliq.SendDM(email, msg); err != nil {
				results["dm_error"] = err.Error()
			} else {
				results["dm"] = "sent to " + email
			}
		}

		ctx.JSON(http.StatusOK, results)
	})

	r.POST("/test/db-connection", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusOK, gin.H{"status": "error", "message": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Connection successful"})
	})

	// Data endpoints
	r.GET("/data/databases", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		databases, err := db.ListDatabases()
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"databases": databases, "count": len(databases)})
	})

	r.GET("/data/tables", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		tables, err := db.GetTablesList()
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"tables": tables, "count": len(tables)})
	})

	r.GET("/data/schema", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		tbl := ctx.DefaultQuery("table", tableName)
		cols, err := explorer.GetSchema(tbl)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{
			"table":   tbl,
			"columns": cols,
			"count":   len(cols),
		})
	})

	r.GET("/data/preview", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		tbl := ctx.DefaultQuery("table", tableName)
		limit := 20
		if l, err := fmt.Sscan(ctx.DefaultQuery("limit", "20"), &limit); err != nil || l == 0 {
			limit = 20
		}
		if limit > 1000 {
			limit = 1000
		}
		result, err := explorer.GetSampleData(tbl, limit)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, result)
	})

	r.GET("/data/columns/:column", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		tbl := ctx.DefaultQuery("table", tableName)
		column := ctx.Param("column")
		values, err := explorer.GetDistinctValues(tbl, column)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{
			"table":  tbl,
			"column": column,
			"values": values,
			"count":  len(values),
		})
	})

	r.GET("/data/duplicate-punches", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		limit := 50
		if l, err := fmt.Sscan(ctx.DefaultQuery("limit", "50"), &limit); err != nil || l == 0 {
			limit = 50
		}
		if limit > 500 {
			limit = 500
		}
		result, err := explorer.DetectDuplicatePunches(tableName, "Devices", limit)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{
			"table":           tableName,
			"columns":         result.Columns,
			"duplicate_count": len(result.Rows),
			"punches":         result.Rows,
		})
	})

	r.GET("/data/long-breaks", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		threshold := 65
		if t, err := fmt.Sscan(ctx.DefaultQuery("minutes", "65"), &threshold); err != nil || t == 0 {
			threshold = 65
		}
		limit := 50
		if l, err := fmt.Sscan(ctx.DefaultQuery("limit", "50"), &limit); err != nil || l == 0 {
			limit = 50
		}
		if limit > 500 {
			limit = 500
		}
		result, err := explorer.DetectLongBreaks(tableName, "Devices", threshold, limit)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{
			"table":         tableName,
			"threshold_min": threshold,
			"work_hours":    "10:15 - 18:00",
			"columns":       result.Columns,
			"break_count":   len(result.Rows),
			"breaks":        result.Rows,
		})
	})

	r.GET("/data/punch-errors", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		limit := 100
		if l, err := fmt.Sscan(ctx.DefaultQuery("limit", "100"), &limit); err != nil || l == 0 {
			limit = 100
		}
		if limit > 500 {
			limit = 500
		}
		threshold := 65
		if t, err := fmt.Sscan(ctx.DefaultQuery("minutes", "65"), &threshold); err != nil || t == 0 {
			threshold = 65
		}
		errors, err := explorer.GetAllPunchErrors(tableName, "Devices", limit, threshold)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{
			"total_errors": len(errors),
			"errors":       errors,
		})
	})

	// People & Setup endpoints
	r.POST("/data/setup", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := empManager.CreateTables(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"status": "ok", "message": "People_Employee, Duplicate_Punches, Long_Breaks tables ready"})
	})

	r.Any("/data/people/sync", func(ctx *gin.Context) {
		dbPath := ctx.DefaultQuery("db", "orgchart.db")
		count, err := empManager.SyncFromSQLite(dbPath)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"status": "ok", "synced": count, "source": dbPath})
	})

	empHandler := handlers.NewEmployeeHandler(empManager)
	r.POST("/api/employees", empHandler.Upsert)

	r.GET("/data/people", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		people, err := empManager.GetAll()
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, gin.H{"people": people, "count": len(people)})
	})

	r.Any("/data/errors/save", func(ctx *gin.Context) {
		if err := getDB(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		var req struct {
			Limit     int `json:"limit"`
			Threshold int `json:"threshold"`
		}
		req.Limit = 100
		req.Threshold = 65
		ctx.ShouldBindJSON(&req)

		errors, err := explorer.GetAllPunchErrors(tableName, "Devices", req.Limit, req.Threshold)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		dupCount, breakCount := empManager.SaveDetectedErrors(errors)

		ctx.JSON(http.StatusOK, gin.H{
			"status":            "ok",
			"duplicate_saved":   dupCount,
			"long_breaks_saved": breakCount,
			"total_detected":    len(errors),
		})
	})

	srv := &http.Server{
		Addr:    ":8030",
		Handler: r,
	}

	go func() {
		slog.Info("server listening on :8030")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
		}
	}()

	watcherCtx, watcherStop := context.WithCancel(context.Background())
	defer watcherStop()

	go func() {
		for {
			if err := getDB(); err != nil {
				slog.Error("watcher: waiting for database", "error", err)
				select {
				case <-watcherCtx.Done():
					return
				case <-time.After(30 * time.Second):
				}
				continue
			}
			break
		}
		hrEmail := os.Getenv("HR_EMAIL")
		var adminEmails []string
		if e1 := os.Getenv("ADMIN_EMAIL1"); e1 != "" {
			adminEmails = append(adminEmails, e1)
		}
		if e2 := os.Getenv("ADMIN_EMAIL2"); e2 != "" {
			adminEmails = append(adminEmails, e2)
		}
		watcher := data.NewPunchWatcher(db, explorer, tableName, "Devices", empManager.SaveDetectedErrors, messaging.DefaultCliq, hrEmail, adminEmails, empManager.GetNameByEmployeeID, func(id string) string { email, _ := empManager.GetEmailByEmployeeID(id); return email })
		watcher.Start(watcherCtx)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server...")

	watcherStop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
	}
}
