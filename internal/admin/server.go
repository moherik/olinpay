package admin

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/pkg/provider"
)

// Deps are the admin server's collaborators.
type Deps struct {
	Engine       *core.Engine
	Registry     *provider.Registry
	AdminUsers   repository.AdminUserRepository
	Sessions     repository.AdminSessionRepository
	Audit        repository.AuditRepository
	APIKeys      repository.APIKeyRepository
	Merchants    repository.MerchantRepository
	Transactions repository.TransactionRepository
	Deliveries   repository.WebhookDeliveryRepository
	Inbound      repository.InboundEventRepository
	ProviderCfgs repository.ProviderConfigRepository
	Stats        repository.AdminStore
	// ReloadProvider rebuilds (or retires) a provider instance after its
	// credentials changed. Provided by the host wiring.
	ReloadProvider func(name, environment string) error
	// FeatureFlags reports build-gated capabilities to the console.
	FeatureFlags func() map[string]bool
	// WebhookMaxAttempts is the configured dispatcher retry budget; requeued
	// deliveries reuse it.
	WebhookMaxAttempts int
	// Pepper is the API-key hashing pepper, shared with the merchant
	// authentication middleware so admin-issued keys verify identically.
	Pepper string
	Log    *slog.Logger
}

// Server is the admin console HTTP server.
type Server struct {
	engine       *core.Engine
	registry     *provider.Registry
	users        repository.AdminUserRepository
	sessions     repository.AdminSessionRepository
	audit        repository.AuditRepository
	apiKeys      repository.APIKeyRepository
	merchants    repository.MerchantRepository
	transactions repository.TransactionRepository
	deliveries   repository.WebhookDeliveryRepository
	inbound      repository.InboundEventRepository
	providerCfgs repository.ProviderConfigRepository
	stats        repository.AdminStore
	reload       func(name, environment string) error
	flags        func() map[string]bool
	maxAttempts  int
	pepper       string
	log          *slog.Logger
	now          func() time.Time
	newID        func(string) string
}

// New builds the admin server.
func New(d Deps) *Server {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	maxAttempts := d.WebhookMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 6
	}
	return &Server{
		engine:       d.Engine,
		registry:     d.Registry,
		users:        d.AdminUsers,
		sessions:     d.Sessions,
		audit:        d.Audit,
		apiKeys:      d.APIKeys,
		merchants:    d.Merchants,
		transactions: d.Transactions,
		deliveries:   d.Deliveries,
		inbound:      d.Inbound,
		providerCfgs: d.ProviderCfgs,
		stats:        d.Stats,
		reload:       d.ReloadProvider,
		flags:        d.FeatureFlags,
		maxAttempts:  maxAttempts,
		pepper:       d.Pepper,
		log:          log,
		now:          time.Now,
		newID:        newPrefixedID,
	}
}

// Handler returns the chi router: public auth endpoints plus the
// session-guarded console API with per-route role requirements.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()

	// Public: authentication and bootstrap status.
	r.Post("/api/login", s.login)
	r.Get("/api/console-status", s.consoleStatus)
	r.Post("/api/setup", s.setup)

	// Everything below requires a live session.
	r.Group(func(r chi.Router) {
		r.Use(s.requireSession)

		r.Get("/api/me", s.me)
		r.Post("/api/logout", s.logout)
		r.Get("/api/overview", s.overview)

		// Monitor.
		r.Get("/api/transactions", s.listTransactions)
		r.Get("/api/transactions/{id}", s.getTransaction)
		r.With(requireRole(domain.RoleOperator)).Post("/api/transactions/{id}/cancel", s.cancelTransaction)

		r.Get("/api/deliveries", s.listDeliveries)
		r.With(requireRole(domain.RoleTech)).Post("/api/deliveries/{id}/retry", s.retryDelivery)

		r.Get("/api/inbound", s.listInbound)
		r.With(requireRole(domain.RoleOperator)).Post("/api/inbound/{id}/reprocess", s.reprocessInbound)

		// Configure.
		r.Get("/api/merchants", s.listMerchants)
		r.With(requireRole(domain.RoleOperator)).Post("/api/merchants", s.createMerchant)
		r.Get("/api/merchants/{id}", s.getMerchant)
		r.With(requireRole(domain.RoleOperator)).Patch("/api/merchants/{id}", s.updateMerchant)
		r.With(requireRole(domain.RoleTech)).Put("/api/merchants/{id}/routing", s.saveRouting)

		r.Get("/api/providers", s.listProviders)
		r.With(requireRole(domain.RoleOwner)).Put("/api/providers/{name}/config", s.saveProviderConfig)
		r.With(requireRole(domain.RoleOwner)).Delete("/api/providers/{name}/config", s.removeProviderConfig)

		// Security.
		r.Get("/api/api-keys", s.listAPIKeys)
		r.With(requireRole(domain.RoleOwner)).Post("/api/api-keys", s.createAPIKey)
		r.With(requireRole(domain.RoleOwner)).Post("/api/api-keys/{id}/revoke", s.revokeAPIKey)

		r.Get("/api/users", s.listUsers)
		r.With(requireRole(domain.RoleOwner)).Post("/api/users", s.createUser)
		r.With(requireRole(domain.RoleOwner)).Patch("/api/users/{id}", s.updateUser)

		r.Get("/api/audit", s.listAudit)
	})

	return r
}
