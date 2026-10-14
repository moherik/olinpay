// Package config loads runtime configuration from the environment, with
// optional .env file support for local development.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/moherik/openpay.id/pkg/provider"
)

// Env variable names (all prefixed with OPENPAY_ to avoid collisions).
const (
	envEnv            = "OPENPAY_ENV"
	envLogLevel       = "OPENPAY_LOG_LEVEL"
	envAPIAddr        = "OPENPAY_API_ADDR"
	envWebhookAddr    = "OPENPAY_WEBHOOK_ADDR"
	envDatabaseURL    = "OPENPAY_DATABASE_URL"
	envShutdown       = "OPENPAY_SHUTDOWN_TIMEOUT"
	envAPIKeyPepper   = "OPENPAY_API_KEY_PEPPER"
	envProviders      = "OPENPAY_PROVIDERS"
	envWebhookTimeout = "OPENPAY_WEBHOOK_TIMEOUT"
	envWebhookRetries = "OPENPAY_WEBHOOK_MAX_ATTEMPTS"
	envWebhookBase    = "OPENPAY_WEBHOOK_BASE_BACKOFF"
	envWebhookMax     = "OPENPAY_WEBHOOK_MAX_BACKOFF"
	envWebhookWorker  = "OPENPAY_WEBHOOK_WORKER_INTERVAL"
	envMigrationDir   = "OPENPAY_MIGRATIONS_DIR"

	// providerConfigPrefix is prepended to the provider name, then the key:
	// OPENPAY_PROVIDER_<NAME>_<KEY>.
	providerConfigPrefix = "OPENPAY_PROVIDER_"
)

// Config is the fully resolved application configuration.
type Config struct {
	// Env is the deployment environment label ("development", "production").
	Env string
	// LogLevel is one of debug|info|warn|error.
	LogLevel string
	// APIAddr is the listen address of the API interface server.
	APIAddr string
	// WebhookAddr is the listen address of the inbound webhook engine server.
	WebhookAddr string
	// DatabaseURL is the PostgreSQL DSN.
	DatabaseURL string
	// APIKeyPepper is an optional secret mixed into API-key hashing.
	APIKeyPepper string
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration
	// MigrationsDir is where .sql migration files live.
	MigrationsDir string
	// Providers lists the provider names to construct at startup.
	Providers []string
	// ProviderConfigs maps provider name -> its key/value configuration.
	ProviderConfigs map[string]provider.Config
	// Webhook tunes outbound delivery to merchant systems.
	Webhook WebhookConfig
}

// WebhookConfig tunes the outbound merchant webhook dispatcher.
type WebhookConfig struct {
	// Timeout is the per-attempt HTTP timeout.
	Timeout time.Duration
	// MaxAttempts is the total delivery attempts before giving up.
	MaxAttempts int
	// BaseBackoff is the delay before the first retry.
	BaseBackoff time.Duration
	// MaxBackoff caps the exponential backoff.
	MaxBackoff time.Duration
	// WorkerInterval is how often the dispatcher polls for pending deliveries.
	WorkerInterval time.Duration
}

// Load reads configuration from .env (when present) and the process
// environment, applying defaults suitable for local development.
func Load() (*Config, error) {
	// Best-effort: a missing .env is not an error.
	_ = godotenv.Load()

	cfg := &Config{
		Env:             getenv(envEnv, "development"),
		LogLevel:        getenv(envLogLevel, "info"),
		APIAddr:         getenv(envAPIAddr, ":8080"),
		WebhookAddr:     getenv(envWebhookAddr, ":8081"),
		DatabaseURL:     getenv(envDatabaseURL, "postgres://openpay:openpay@localhost:5432/openpay?sslmode=disable"),
		APIKeyPepper:    os.Getenv(envAPIKeyPepper),
		MigrationsDir:   getenv(envMigrationDir, "migrations"),
		ProviderConfigs: make(map[string]provider.Config),
	}

	var err error
	if cfg.ShutdownTimeout, err = getduration(envShutdown, 15*time.Second); err != nil {
		return nil, err
	}
	if cfg.Webhook.Timeout, err = getduration(envWebhookTimeout, 10*time.Second); err != nil {
		return nil, err
	}
	if cfg.Webhook.BaseBackoff, err = getduration(envWebhookBase, 5*time.Second); err != nil {
		return nil, err
	}
	if cfg.Webhook.MaxBackoff, err = getduration(envWebhookMax, 5*time.Minute); err != nil {
		return nil, err
	}
	if cfg.Webhook.WorkerInterval, err = getduration(envWebhookWorker, 5*time.Second); err != nil {
		return nil, err
	}
	if cfg.Webhook.MaxAttempts, err = getint(envWebhookRetries, 6); err != nil {
		return nil, err
	}

	cfg.Providers = splitList(os.Getenv(envProviders))
	for _, name := range cfg.Providers {
		cfg.ProviderConfigs[name] = collectProviderConfig(name)
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("config: %s must not be empty", envDatabaseURL)
	}
	return cfg, nil
}

// collectProviderConfig scans the environment for OPENPAY_PROVIDER_<NAME>_*
// variables and returns the stripped, lower-cased key/value pairs.
func collectProviderConfig(name string) provider.Config {
	prefix := providerConfigPrefix + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
	out := provider.Config{}
	for _, kv := range os.Environ() {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(key, prefix) {
			continue
		}
		out[strings.ToLower(strings.TrimPrefix(key, prefix))] = value
	}
	return out
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func getduration(key string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if strings.TrimSpace(raw) == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: invalid %s=%q: %w", key, raw, err)
	}
	return d, nil
}

func getint(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if strings.TrimSpace(raw) == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: invalid %s=%q: %w", key, raw, err)
	}
	return n, nil
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
