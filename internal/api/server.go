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

// Server holds the API handlers' dependencies.
type Server struct {
	engine    *core.Engine
	merchants repository.MerchantRepository
	pepper    string
	log       *slog.Logger
}

// NewServer builds the API server.
func NewServer(engine *core.Engine, merchants repository.MerchantRepository, pepper string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{engine: engine, merchants: merchants, pepper: pepper, log: log}
}

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

	return r
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	// The engine is wired at startup; if we are serving, we are ready.
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
