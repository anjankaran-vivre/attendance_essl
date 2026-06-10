package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

func init() {
	loadEnv()
}

// ---- .env loader ----

func loadEnv() {
	cwd, _ := os.Getwd()
	exe, _ := os.Executable()

	paths := []string{cwd + string(os.PathSeparator) + ".env"}
	if idx := strings.LastIndex(exe, string(os.PathSeparator)); idx >= 0 {
		paths = append(paths, exe[:idx]+string(os.PathSeparator)+".env")
	}

	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key != "" {
				os.Setenv(key, val)
			}
		}
		return
	}
	fmt.Println("WARNING: .env file not found")
}

// ---- helpers ----

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

// ---- MSSQL ----

type MSSQLConfig struct {
	Host     string
	Port     string
	Database string
	Username string
	Password string
}

var (
	mssqlOnce sync.Once
	mssqlCfg  *MSSQLConfig
)

func GetMSSQL() *MSSQLConfig {
	mssqlOnce.Do(func() {
		mssqlCfg = &MSSQLConfig{
			Host:     env("MSSQL_HOST", ""),
			Port:     env("MSSQL_PORT", "1433"),
			Database: env("MSSQL_DATABASE", ""),
			Username: env("MSSQL_USERNAME", ""),
			Password: env("MSSQL_PASSWORD", ""),
		}
	})
	return mssqlCfg
}

func (c *MSSQLConfig) ConnectionString() string {
	return c.ConnectionStringForDB(c.Database)
}

func (c *MSSQLConfig) ConnectionStringForDB(database string) string {
	return fmt.Sprintf(
		"server=%s;port=%s;user id=%s;password=%s;database=%s;trustservercertificate=true",
		strings.TrimRight(c.Host, ","), c.Port, c.Username, c.Password, database,
	)
}

func DebugConnString() string {
	cfg := GetMSSQL()
	host := strings.TrimRight(cfg.Host, ",")
	pw := cfg.Password
	if len(pw) > 4 {
		pw = pw[:2] + "****" + pw[len(pw)-2:]
	}
	return fmt.Sprintf(
		"server=%s;port=%s;user id=%s;password=%s;database=%s;trustservercertificate=true",
		host, cfg.Port, cfg.Username, pw, cfg.Database,
	)
}

// ---- EMP (Employee Database) ----

type EMPConfig struct {
	Host     string
	Server   string
	Database string
	Username string
	Password string
}

var (
	empOnce sync.Once
	empCfg  *EMPConfig
)

func GetEMP() *EMPConfig {
	empOnce.Do(func() {
		empCfg = &EMPConfig{
			Host:     env("EMP_HOST", "localhost"),
			Server:   env("EMP_SERVER", ""),
			Database: env("EMP_DATABASE", "people_employee"),
			Username: env("EMP_USERNAME", "sa"),
			Password: env("EMP_PASSWORD", ""),
		}
	})
	return empCfg
}

func (c *EMPConfig) ConnectionString() string {
	server := c.Host
	if c.Server != "" {
		server += "\\" + c.Server
	}
	return fmt.Sprintf(
		"server=%s;user id=%s;password=%s;database=%s;trustservercertificate=true",
		server, c.Username, c.Password, c.Database,
	)
}

// ---- Cliq ----

type CliqConfig struct {
	Token  string
	HookID string
}

var (
	cliqOnce sync.Once
	cliqCfg  *CliqConfig
)

func GetCliq() *CliqConfig {
	cliqOnce.Do(func() {
		cliqCfg = &CliqConfig{
			Token:  env("CLIQ_WEBHOOK_TOKEN", ""),
			HookID: env("CLIQ_HOOK_ID", "infobot"),
		}
	})
	return cliqCfg
}

