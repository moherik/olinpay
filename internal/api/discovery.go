package api

import (
	"net/http"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/pkg/provider"
)

// providerInfo is the discovery view of a registered provider.
type providerInfo struct {
	Name     string            `json:"name"`
	Methods  []provider.Method `json:"methods,omitempty"`
	Channels []string          `json:"channels,omitempty"`
	Inquiry  bool              `json:"supports_inquiry"`
	Cancel   bool              `json:"supports_cancel"`
	Webhook  bool              `json:"supports_webhook"`
	Sandbox  bool              `json:"supports_sandbox"`
}

// listProviders handles GET /v1/providers, letting integrators discover which
// gateways are available and what they support.
func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	registry := s.engine.Registry()
	out := make([]providerInfo, 0, registry.Len())
	for _, name := range registry.Names() {
		p, ok := registry.Get(name)
		if !ok {
			continue
		}
		caps := p.Capabilities()
		out = append(out, providerInfo{
			Name:     name,
			Methods:  caps.Methods,
			Channels: caps.Channels,
			Inquiry:  caps.SupportsInquiry,
			Cancel:   caps.SupportsCancel,
			Webhook:  caps.SupportsWebhook,
			Sandbox:  caps.SupportsSandbox,
		})
	}
	writeData(w, http.StatusOK, out, map[string]any{"count": len(out)})
}

// getMerchant handles GET /v1/me and returns the authenticated account's public
// profile (never the secret material).
func (s *Server) getMerchant(w http.ResponseWriter, r *http.Request) {
	merchant, ok := merchantFromContext(r.Context())
	if !ok {
		fail(w, r, domain.ErrUnauthorized, "")
		return
	}
	writeData(w, http.StatusOK, merchant, nil)
}
