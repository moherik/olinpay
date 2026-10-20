// Command openpay is the single binary that runs the whole orchestration
// platform.
//
// Subcommands:
//
//	openpay serve     # run the API interface and webhook engine servers
//	openpay migrate   # apply SQL migrations
//	openpay seed      # create a merchant (API consumer) and print its API key
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/moherik/openpay.id/internal/admin"
	"github.com/moherik/openpay.id/internal/api"
	"github.com/moherik/openpay.id/internal/config"
	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/id"
	"github.com/moherik/openpay.id/internal/platform/logger"
	"github.com/moherik/openpay.id/internal/platform/migrate"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/internal/repository/postgres"
	"github.com/moherik/openpay.id/internal/webhook"
	"github.com/moherik/openpay.id/pkg/provider"

	// Provider integrations register their factories via init(). Importing the
	// package for side effects is all that is required to make them available.
	_ "github.com/moherik/openpay.id/pkg/provider/doku"
	_ "github.com/moherik/openpay.id/pkg/provider/midtrans"
	_ "github.com/moherik/openpay.id/pkg/provider/midtrans-snap"
	_ "github.com/moherik/openpay.id/pkg/provider/mock"
	_ "github.com/moherik/openpay.id/pkg/provider/winpay"
	_ "github.com/moherik/openpay.id/pkg/provider/xendit"
)

// version is overridable at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "openpay: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	switch cmd {
	case "serve":
		return serve()
	case "migrate":
		return runMigrations()
	case "seed":
		return seed(args)
	case "version", "-version", "--version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `openpay.id - open source payment orchestration for Indonesia

Usage:
	openpay serve        Run the API, admin console and webhook engine servers
	openpay migrate      Apply SQL migrations to the configured database
	openpay seed         Create a merchant and print its API key
	openpay version      Print the version

Configuration is read from the environment (see .env.example).
`)
}

// --- serve -----------------------------------------------------------------

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg.LogLevel, os.Stdout)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	txRepo := postgres.NewTransactionRepository(pool)
	merchantRepo := postgres.NewMerchantRepository(pool)
	deliveryRepo := postgres.NewWebhookRepository(pool)
	adminUserRepo := postgres.NewAdminUserRepository(pool)
	adminSessionRepo := postgres.NewAdminSessionRepository(pool)
	auditRepo := postgres.NewAuditRepository(pool)
	apiKeysRepo := postgres.NewAPIKeyRepository(pool)
	inboundRepo := postgres.NewInboundEventRepository(pool)
	providerCfgRepo := postgres.NewProviderConfigRepository(pool)
	adminStats := postgres.NewAdminStore(pool)

	// The registry is built environment-first, then DB-stored credentials are
	// instantiated on top: a deployment relying only on console-saved
	// credentials therefore boots with an empty OPENPAY_PROVIDERS.
	registry, err := buildRegistry(cfg, providerCfgRepo, log)
	if err != nil {
		return err
	}

	// Webhook engine (outbound) doubles as the core engine's notifier.
	dispatcher := webhook.NewDispatcher(deliveryRepo, merchantRepo, cfg.Webhook, log)

	engine := core.New(core.Deps{
		Registry:     registry,
		Router:       core.NewRouter(registry),
		Transactions: txRepo,
		Merchants:    merchantRepo,
		Notifier:     dispatcher,
		Logger:       log,
	})

	receiver := webhook.NewReceiver(registry, engine, inboundRepo, log)
	receiver.SetEnvironmentResolver(providerEnvironments(providerCfgRepo, registry))

	// Provider hot reload: the admin console saves credentials, this callback
	// rebuilds the live instance and refreshes the receiver's environment map.
	reloadProvider := func(adapter, env string) error {
		if err := applyProviderConfig(providerCfgRepo, registry, adapter, env); err != nil {
			return err
		}
		receiver.SetEnvironmentResolver(providerEnvironments(providerCfgRepo, registry))
		return nil
	}

	adminServer := admin.New(admin.Deps{
		Engine:             engine,
		Registry:           registry,
		AdminUsers:         adminUserRepo,
		Sessions:           adminSessionRepo,
		Audit:              auditRepo,
		APIKeys:            apiKeysRepo,
		Merchants:          merchantRepo,
		Transactions:       txRepo,
		Deliveries:         deliveryRepo,
		Inbound:            inboundRepo,
		ProviderCfgs:       providerCfgRepo,
		Stats:              adminStats,
		ReloadProvider:     reloadProvider,
		FeatureFlags:       featureFlags,
		WebhookMaxAttempts: cfg.Webhook.MaxAttempts,
		Pepper:             cfg.APIKeyPepper,
		Log:                log,
	})

	if n, err := adminUserRepo.Count(ctx); err == nil && n == 0 {
		log.Warn("no console users exist; complete first-owner setup at /setup")
	}

	apiServer := api.NewServer(engine, merchantRepo, apiKeysRepo, cfg.APIKeyPepper, log)
	apiServer.MountAdmin(adminServer.Handler())
	webhookRouter := chi.NewRouter()
	webhookRouter.Use(middleware.RequestID)
	webhookRouter.Use(middleware.RealIP)
	webhookRouter.Use(middleware.Recoverer)
	webhookRouter.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	receiver.Mount(webhookRouter)

	apiSrv := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           apiServer.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	webhookSrv := &http.Server{
		Addr:              cfg.WebhookAddr,
		Handler:           webhookRouter,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Info("openpay.id starting",
		"version", version,
		"env", cfg.Env,
		"providers", strings.Join(registry.Names(), ","),
	)

	errCh := make(chan error, 2)
	go listen(log, apiSrv, "api", errCh)
	go listen(log, webhookSrv, "webhook", errCh)
	go dispatcher.Run(ctx)

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	_ = apiSrv.Shutdown(shutdownCtx)
	_ = webhookSrv.Shutdown(shutdownCtx)
	return nil
}

func listen(log *slog.Logger, srv *http.Server, name string, errCh chan<- error) {
	log.Info("server listening", "server", name, "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- fmt.Errorf("%s server: %w", name, err)
		return
	}
	errCh <- nil
}

// buildProviders constructs every provider listed in configuration by looking
// up its registered factory.
func buildProviders(cfg *config.Config, log *slog.Logger) (*provider.Registry, error) {
	// Route every outbound provider API call through the application logger so
	// gateway requests and responses are traceable.
	provider.SetHTTPLogger(log)

	registry := provider.NewRegistry()
	for _, name := range cfg.Providers {
		if _, err := registry.Build(name, cfg.ProviderConfigs[name]); err != nil {
			return nil, fmt.Errorf("building provider %q: %w", name, err)
		}
		log.Info("provider registered", "name", name)
	}
	return registry, nil
}

// buildRegistry assembles the provider registry: every provider listed in
// OPENPAY_PROVIDERS is built from the environment, then DB-stored credentials
// (console-saved) are instantiated on top. The empty-registry check runs after
// the database build so a console-managed deployment boots without
// OPENPAY_PROVIDERS at all.
func buildRegistry(cfg *config.Config, repo repository.ProviderConfigRepository, log *slog.Logger) (*provider.Registry, error) {
	registry, err := buildProviders(cfg, log)
	if err != nil {
		return nil, err
	}
	buildProviderFromDB(repo, registry, log)
	if registry.Len() == 0 {
		return nil, errors.New(
			"no providers configured; register one in the admin console (Providers page) or set OPENPAY_PROVIDERS (e.g. \"mock\")")
	}
	return registry, nil
}

// buildProviderFromDB instantiates every DB-stored credential set on top of
// the environment-built instances. A DB row targeting the same instance name
// as an env-configured provider overrides it (documented behavior: the console
// is the newer control plane).
func buildProviderFromDB(repo repository.ProviderConfigRepository, registry *provider.Registry, log *slog.Logger) {
	rows, err := repo.List(context.Background())
	if err != nil {
		log.Warn("provider credential store unavailable; skipping DB providers", "error", err.Error())
		return
	}
	for _, row := range rows {
		if err := applyProviderConfig(repo, registry, row.Provider, row.Environment); err != nil {
			log.Error("provider instance failed to build from stored credentials",
				"provider", row.Provider, "environment", row.Environment, "error", err.Error())
			continue
		}
		log.Info("provider registered from stored credentials",
			"name", domain.InstanceName(row.Provider, row.Environment), "environment", row.Environment)
	}
}

// applyProviderConfig builds (or rebuilds) one provider instance from its
// stored credential row. A missing row retires the instance unless it is
// env-configured under the same name.
func applyProviderConfig(repo repository.ProviderConfigRepository, registry *provider.Registry, adapter, env string) error {
	row, err := repo.Get(context.Background(), adapter, env)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Credentials were removed. Remove the DB-built instance; an
			// env-built instance of the same name is left untouched only if
			// the adapter appears in OPENPAY_PROVIDERS, which we cannot see
			// here — removal is the safe default, credentials can be saved
			// again.
			registry.Remove(domain.InstanceName(adapter, env))
			return nil
		}
		return err
	}
	if _, err := registry.BuildAs(domain.InstanceName(adapter, env), adapter, provider.Config(row.Config)); err != nil {
		return fmt.Errorf("building %q (%s): %w", adapter, env, err)
	}
	return nil
}

// providerEnvironments resolves the environment each provider instance aims
// at, for tagging inbound callbacks that fail to correlate with a
// transaction. Registry-built (env-var) instances resolve to "" — unknown —
// which the console always lists.
func providerEnvironments(repo repository.ProviderConfigRepository, registry *provider.Registry) func(string) string {
	envByInstance := map[string]string{}
	for _, row := range fetchProviderConfigs(repo) {
		envByInstance[domain.InstanceName(row.Provider, row.Environment)] = row.Environment
	}
	return func(instance string) string { return envByInstance[instance] }
}

func fetchProviderConfigs(repo repository.ProviderConfigRepository) []*domain.ProviderConfig {
	rows, err := repo.List(context.Background())
	if err != nil {
		return nil
	}
	return rows
}

// featureFlags reports which console capabilities this build honestly backs.
// Anything absent is rendered disabled by the dashboard instead of
// interacting with data that does not exist.
func featureFlags() map[string]bool {
	return map[string]bool{
		// Live, engine-backed capabilities.
		"transactions":      true,
		"cancel":            true,
		"webhook_retry":     true,
		"inbound_reprocess": true,
		"merchants":         true,
		"routing":           true,
		"api_keys":          true,
		"users":             true,
		"providers":         true,
		"provider_config":   true,
		"audit":             true,
		// Gated: no backing flow in this build yet.
		"refund":             false,
		"transaction_events": false,
		"provider_debug_log": false,
	}
}

// --- migrate ---------------------------------------------------------------

func runMigrations() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg.LogLevel, os.Stdout)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := migrate.Run(ctx, pool, cfg.MigrationsDir, log); err != nil {
		return err
	}
	log.Info("migrations complete")
	return nil
}

// --- seed ------------------------------------------------------------------

func seed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	name := fs.String("name", "Demo Merchant", "merchant display name")
	callbackURL := fs.String("callback-url", "", "merchant webhook callback URL")
	webhookSecret := fs.String("webhook-secret", "", "secret used to sign outbound webhooks (generated when empty)")
	apiKey := fs.String("api-key", "", "explicit API key (generated when empty)")
	providersFlag := fs.String("providers", "", "comma separated provider names (default: all registered)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := logger.New(cfg.LogLevel, os.Stdout)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	resolvedKey := *apiKey
	if resolvedKey == "" {
		if resolvedKey, err = id.GenerateAPIKey("sk_test"); err != nil {
			return err
		}
	}
	resolvedSecret := *webhookSecret
	if resolvedSecret == "" {
		if resolvedSecret, err = id.GenerateAPIKey("whsec"); err != nil {
			return err
		}
	}

	routing := domain.RoutingConfig{Strategy: domain.StrategyPriority}
	for i, p := range splitCSV(*providersFlag) {
		routing.Providers = append(routing.Providers, domain.RoutingProvider{
			Name:     p,
			Priority: i,
			Enabled:  true,
		})
	}

	now := time.Now()
	merchantID, err := id.NewMerchantID()
	if err != nil {
		return err
	}
	merchant := &domain.Merchant{
		ID:            merchantID,
		Name:          *name,
		APIKeyHash:    id.HashAPIKey(resolvedKey, cfg.APIKeyPepper),
		APIKeyPrefix:  id.APIKeyPrefix(resolvedKey),
		CallbackURL:   *callbackURL,
		WebhookSecret: resolvedSecret,
		Routing:       routing,
		Status:        domain.MerchantActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := postgres.NewMerchantRepository(pool).Create(ctx, merchant); err != nil {
		return err
	}

	log.Info("merchant created", "merchant_id", merchant.ID, "name", merchant.Name)
	fmt.Printf("\n  Merchant ID      : %s\n  API key          : %s\n  Webhook secret   : %s\n\n",
		merchant.ID, resolvedKey, resolvedSecret)
	fmt.Println("  Store the API key and webhook secret now; they are not shown again.")
	return nil
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
