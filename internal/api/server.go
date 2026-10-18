// Package api implements the API interface: the single HTTP surface that
// merchant systems integrate against. It translates HTTP requests into core
// engine commands and maps the resulting errors onto sensible status codes.
package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/repository"
)

type Server struct {
	engine    *core.Engine
	merchants repository.MerchantRepository
	apiKeys   repository.APIKeyRepository
	admin     http.Handler
	pepper    string
	log       *slog.Logger
}

// NewServer builds the API server.
func NewServer(engine *core.Engine, merchants repository.MerchantRepository, apiKeys repository.APIKeyRepository, pepper string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{engine: engine, merchants: merchants, apiKeys: apiKeys, pepper: pepper, log: log}
}

// Admin mounts an optional admin console handler under /admin on the API
// router. The handler is expected to bring its own session auth; when nil the
// admin surface is simply absent.
func (s *Server) MountAdmin(h http.Handler) { s.admin = h }

// Router builds the chi router exposing the public API.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.requestLogger)

	// Unauthenticated operational endpoints.
	r.Get("/healthz", s.health)
	r.Get("/readyz", s.ready)
	r.Get("/docs", s.swaggerUI)
	r.Get("/openapi.yaml", s.openAPISpec)

	// Authenticated API.
	r.Route("/v1", func(r chi.Router) {
		r.Use(s.authenticate)

		r.Get("/me", s.getMerchant)
		r.Get("/providers", s.listProviders)

		r.Route("/payments", func(r chi.Router) {
			r.Post("/", s.createPayment)
			r.Get("/", s.listPayments)
			r.Get("/{id}", s.getPayment)
			r.Post("/{id}/cancel", s.cancelPayment)
		})
	})

	// Admin console API, when wired. The handler carries its own auth
	// middleware (session cookie based, separate from merchant API keys).
	if s.admin != nil {
		r.Mount("/admin", s.admin)
	}

	return r
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	// The engine is wired at startup; if we are serving, we are ready.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
