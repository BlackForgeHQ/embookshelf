// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SharedS3Config carries the bucket-level S3 configuration shared by
// every s3-kind library in the deployment. Env-driven; not editable
// from the UI. Per-library prefix is computed from libraries.slug.
type SharedS3Config struct {
	Bucket          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

// Configured reports whether the shared bucket is set. Used by the
// library-create handler to gate the kind=s3 path.
func (s SharedS3Config) Configured() bool {
	return strings.TrimSpace(s.Bucket) != ""
}

type Config struct {
	Port             int
	DatabaseURL      string
	DatabaseMaxConns int32
	DatabaseMinConns int32
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

	// SharedS3 is the shared bucket configuration for libraries created with
	// kind=s3. Populated from EMBOOKSHELF_S3_* env vars. When Bucket is
	// empty, s3-kind library creation is disabled.
	SharedS3 SharedS3Config

	// PresignTTL is the lifetime of presigned URLs issued for S3-backed book
	// files. Default is 10 minutes. Configurable via EMBOOKSHELF_PRESIGN_TTL
	// (any value parseable by time.ParseDuration, e.g. "15m", "1h").
	PresignTTL time.Duration
	// S3RenameGrace is the grace window before the orphaned-keys
	// sweeper deletes old keys left behind by an S3 folder rename
	// (ADR-0005). Zero means "compute at boot as max(2 × PresignTTL,
	// 1h)". Override via EMBOOKSHELF_S3_RENAME_GRACE.
	S3RenameGrace time.Duration

	// PresignFallback selects the delivery mode for book files when
	// the backend supports presign. Set to "presign" to opt into 302
	// redirects to a pre-signed URL — requires the bucket to allow the
	// SPA origin via CORS, otherwise epub.js / pdf.js XHRs fail on
	// the cross-origin redirect. Default ("" or "stream") streams the
	// bytes through the app server, which always works.
	PresignFallback string

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
	// Shared S3 bucket config. Region defaults to "us-east-1" only when
	// bucket is set (matching Plan F's per-backend behaviour).
	s3Bucket := envStr("EMBOOKSHELF_S3_BUCKET", "")
	s3Region := envStr("EMBOOKSHELF_S3_REGION", "")
	if s3Bucket != "" && s3Region == "" {
		s3Region = "us-east-1"
	}

	cfg := Config{
		Port:             envInt("EMBOOKSHELF_PORT", 6060),
		DatabaseURL:      envStr("DATABASE_URL", "sqlite://./data/embookshelf.db"),
		DatabaseMaxConns: int32(envInt("DATABASE_MAX_CONNS", 20)),
		DatabaseMinConns: int32(envInt("DATABASE_MIN_CONNS", 5)),
		AllowedOrigins:   strings.Split(envStr("ALLOWED_ORIGINS", "*"), ","),
		SessionSecret:    envStr("SESSION_SECRET", ""),
		LogLevel:         envStr("LOG_LEVEL", "info"),
		BookDropPath:     envStr("BOOKDROP_PATH", "./bookdrop"),
		BookDropInterval: time.Duration(envInt("BOOKDROP_POLL_SECONDS", 5)) * time.Second,
		DataPath:         envStr("DATA_PATH", "./data"),
		MigrateOnStart:   envBool("MIGRATE_ON_START", true),

		AppURL:    strings.TrimRight(envStr("APP_URL", ""), "/"),
		SecretKey: envStr("EMBOOKSHELF_SECRET_KEY", ""),

		SharedS3: SharedS3Config{
			Bucket:          s3Bucket,
			Region:          s3Region,
			Endpoint:        envStr("EMBOOKSHELF_S3_ENDPOINT", ""),
			AccessKeyID:     envStr("EMBOOKSHELF_S3_ACCESS_KEY_ID", ""),
			SecretAccessKey: envStr("EMBOOKSHELF_S3_SECRET_ACCESS_KEY", ""),
			ForcePathStyle:  envBool("EMBOOKSHELF_S3_FORCE_PATH_STYLE", false),
		},

		PresignTTL:      envDuration("EMBOOKSHELF_PRESIGN_TTL", 10*time.Minute),
		PresignFallback: envStr("EMBOOKSHELF_PRESIGN_FALLBACK", ""),
		S3RenameGrace:   envDuration("EMBOOKSHELF_S3_RENAME_GRACE", 0),

		OTELEnabled:     envBool("OTEL_ENABLED", false),
		OTELServiceName: envStr("OTEL_SERVICE_NAME", "embookshelf"),
		OTELEndpoint:    envStr("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTELProtocol:    strings.ToLower(envStr("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")),
		OTELInsecure:    envBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		OTELSampleRatio: envFloat("OTEL_TRACES_SAMPLER_ARG", 1.0),
	}

	if cfg.OTELEnabled {
		switch cfg.OTELProtocol {
		case "grpc", "http/protobuf":
		default:
			return cfg, errors.New("OTEL_EXPORTER_OTLP_PROTOCOL must be 'grpc' or 'http/protobuf'")
		}
	}

	// LocalFS is rooted at "/" (storageloader), so every caller must hand it
	// absolute paths. The bookdrop watcher records whatever WalkDir yields,
	// which is relative when BOOKDROP_PATH itself is relative — resolve once
	// here so item.Path lands in the DB as absolute.
	if cfg.BookDropPath != "" {
		if abs, err := filepath.Abs(cfg.BookDropPath); err == nil {
			cfg.BookDropPath = abs
		}
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

func envFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
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
