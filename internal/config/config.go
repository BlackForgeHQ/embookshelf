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
	MigrateOnStart   bool
	// EnrichmentProviders is the comma-separated list of provider ids
	// to fan metadata queries across. Default keeps the original two
	// (google_books, open_library) — operators opt in to amazon and
	// duckduckgo by overriding ENRICHMENT_PROVIDERS.
	EnrichmentProviders []string

	// OIDC configuration. When OIDCIssuerURL and OIDCClientID are both
	// non-empty the "Sign in with SSO" flow is enabled.
	OIDCIssuerURL   string
	OIDCClientID    string
	OIDCClientSecret string
	OIDCRedirectURL string
}

func Load() (Config, error) {
	cfg := Config{
		Port:                envInt("EMBOOKSHELF_PORT", 6060),
		DatabaseURL:         envStr("DATABASE_URL", "postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable"),
		DatabaseMaxConns:    int32(envInt("DATABASE_MAX_CONNS", 20)),
		DatabaseMinConns:    int32(envInt("DATABASE_MIN_CONNS", 5)),
		DiskType:            envStr("DISK_TYPE", "LOCAL"),
		AllowedOrigins:      strings.Split(envStr("ALLOWED_ORIGINS", "*"), ","),
		SessionSecret:       envStr("SESSION_SECRET", ""),
		LogLevel:            envStr("LOG_LEVEL", "info"),
		BookDropPath:        envStr("BOOKDROP_PATH", "./bookdrop"),
		BookDropInterval:    time.Duration(envInt("BOOKDROP_POLL_SECONDS", 5)) * time.Second,
		DataPath:            envStr("DATA_PATH", "./data"),
		MigrateOnStart:      envBool("MIGRATE_ON_START", true),
		EnrichmentProviders: envCSV("ENRICHMENT_PROVIDERS", "google_books,open_library"),

		OIDCIssuerURL:    envStr("OIDC_ISSUER_URL", ""),
		OIDCClientID:     envStr("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: envStr("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:  envStr("OIDC_REDIRECT_URL", ""),
	}

	if cfg.DiskType != "LOCAL" && cfg.DiskType != "NETWORK" {
		return cfg, errors.New("DISK_TYPE must be LOCAL or NETWORK")
	}

	// Validate OIDC: if issuer is set, client ID is required.
	if cfg.OIDCIssuerURL != "" && cfg.OIDCClientID == "" {
		return cfg, errors.New("OIDC_CLIENT_ID is required when OIDC_ISSUER_URL is set")
	}

	return cfg, nil
}

// OIDCEnabled reports whether OIDC login is configured.
func (c Config) OIDCEnabled() bool {
	return c.OIDCIssuerURL != "" && c.OIDCClientID != ""
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

// envCSV returns a trimmed, deduped list parsed from a comma-separated
// env value. Empty entries are dropped; falls back to `def` (also
// comma-separated) when the variable is unset or blank.
func envCSV(key, def string) []string {
	raw := envStr(key, def)
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}
