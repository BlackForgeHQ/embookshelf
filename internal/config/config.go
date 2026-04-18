package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port             int
	DatabaseURL      string
	DatabaseMaxConns int32
	DatabaseMinConns int32
	DiskType         string
	AllowedOrigins   []string
	SessionSecret    string
	LogLevel         string
	BookDropPath     string
	BookDropInterval time.Duration
	DataPath         string
}

func Load() (Config, error) {
	cfg := Config{
		Port:             envInt("EMBOOKSHELF_PORT", 6060),
		DatabaseURL:      envStr("DATABASE_URL", "postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable"),
		DatabaseMaxConns: int32(envInt("DATABASE_MAX_CONNS", 20)),
		DatabaseMinConns: int32(envInt("DATABASE_MIN_CONNS", 5)),
		DiskType:         envStr("DISK_TYPE", "LOCAL"),
		AllowedOrigins:   strings.Split(envStr("ALLOWED_ORIGINS", "*"), ","),
		SessionSecret:    envStr("SESSION_SECRET", ""),
		LogLevel:         envStr("LOG_LEVEL", "info"),
		BookDropPath:     envStr("BOOKDROP_PATH", "./bookdrop"),
		BookDropInterval: time.Duration(envInt("BOOKDROP_POLL_SECONDS", 5)) * time.Second,
		DataPath:         envStr("DATA_PATH", "./data"),
	}

	if cfg.DiskType != "LOCAL" && cfg.DiskType != "NETWORK" {
		return cfg, errors.New("DISK_TYPE must be LOCAL or NETWORK")
	}
	return cfg, nil
}

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
