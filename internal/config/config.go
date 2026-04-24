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

	// AppURL is the public origin of the BookLore instance. It feeds
	// the OIDC redirect URI (${AppURL}/api/v1/auth/oidc/callback) and
	// anything else that needs an absolute link back to the UI.
	AppURL string

	// SecretKey is the KEK (base64-encoded 32 bytes) used to encrypt
	// provider API keys and cookies at rest in provider_settings.
	// Unset = plaintext storage (dev mode); the server logs a warning
	// on boot so the fact doesn't silently linger.
	SecretKey string

	// OIDC seed values. These are applied to app_settings on first
	// boot only — the DB is authoritative after that so admins can
	// edit config in the UI without redeploying.
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string

	// OpenTelemetry / OTLP. When OTELEnabled is true the server exports
	// traces, metrics, and logs via OTLP to OTELEndpoint. The SDK also
	// honors standard OTEL_* env vars (OTEL_EXPORTER_OTLP_ENDPOINT,
	// OTEL_RESOURCE_ATTRIBUTES, ...) if set.
	OTELEnabled     bool
	OTELServiceName string
	OTELEndpoint    string
	OTELProtocol    string
	OTELInsecure    bool
	OTELSampleRatio float64
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
		MigrateOnStart:   envBool("MIGRATE_ON_START", true),

		AppURL:    strings.TrimRight(envStr("APP_URL", ""), "/"),
		SecretKey: envStr("EMBOOKSHELF_SECRET_KEY", ""),

		OIDCIssuerURL:    envStr("OIDC_ISSUER_URL", ""),
		OIDCClientID:     envStr("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: envStr("OIDC_CLIENT_SECRET", ""),

		OTELEnabled:     envBool("OTEL_ENABLED", false),
		OTELServiceName: envStr("OTEL_SERVICE_NAME", "embookshelf"),
		OTELEndpoint:    envStr("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTELProtocol:    strings.ToLower(envStr("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")),
		OTELInsecure:    envBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		OTELSampleRatio: envFloat("OTEL_TRACES_SAMPLER_ARG", 1.0),
	}

	if cfg.DiskType != "LOCAL" && cfg.DiskType != "NETWORK" {
		return cfg, errors.New("DISK_TYPE must be LOCAL or NETWORK")
	}

	// Validate OIDC: if issuer is set, client ID is required.
	if cfg.OIDCIssuerURL != "" && cfg.OIDCClientID == "" {
		return cfg, errors.New("OIDC_CLIENT_ID is required when OIDC_ISSUER_URL is set")
	}

	if cfg.OTELEnabled {
		switch cfg.OTELProtocol {
		case "grpc", "http/protobuf":
		default:
			return cfg, errors.New("OTEL_EXPORTER_OTLP_PROTOCOL must be 'grpc' or 'http/protobuf'")
		}
	}

	return cfg, nil
}

// HasOIDCEnvSeed reports whether the legacy env vars carry enough to
// pre-populate app_settings on first boot.
func (c Config) HasOIDCEnvSeed() bool {
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

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
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
