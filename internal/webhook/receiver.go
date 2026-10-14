// Package webhook implements the webhook engine: the inbound receiver that
// accepts callbacks from payment gateways and the outbound dispatcher that
// notifies merchant systems.
//
// Inbound flow (gateway -> openpay.id):
//
//	gateway -> POST /webhooks/{provider} -> Receiver
//	        -> provider.VerifyWebhookSignature (optional)
//	        -> provider.ParseWebhook
//	        -> core.Engine.HandleProviderEvent
//
// Outbound flow (openpay.id -> merchant):
//
//	core.Engine -> Dispatcher.Notify (persist) -> Dispatcher.Run (deliver+retry)
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/platform/httpx"
	"github.com/moherik/openpay.id/pkg/provider"
)

// maxCallbackBytes caps the size of an inbound gateway callback.
const maxCallbackBytes = 2 << 20 // 2 MiB

// Receiver is the inbound webhook engine.
type Receiver struct {
	registry *provider.Registry
	engine   *core.Engine
	log      *slog.Logger
	now      func() time.Time
}

// NewReceiver builds the inbound webhook engine.
func NewReceiver(registry *provider.Registry, engine *core.Engine, log *slog.Logger) *Receiver {
	if log == nil {
		log = slog.Default()
	}
	return &Receiver{registry: registry, engine: engine, log: log, now: time.Now}
}

// Mount registers the callback route on a chi router.
//
// A single path serves every gateway; the provider is identified by the final
// path segment, e.g. POST /webhooks/mock.
func (r *Receiver) Mount(router chi.Router) {
	router.Post("/webhooks/{provider}", r.Handle)
}

// Handle processes one inbound gateway callback.
func (r *Receiver) Handle(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "provider")
	p, ok := r.registry.Get(name)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "provider_not_found",
			"no provider is registered under "+name, "")
		return
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, maxCallbackBytes+1))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_body", "cannot read callback body", "")
		return
	}
	if len(body) > maxCallbackBytes {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "callback body too large", "")
		return
	}

	wreq := &provider.WebhookRequest{
		Headers:    req.Header,
		Body:       body,
		Query:      req.URL.Query(),
		RemoteAddr: req.RemoteAddr,
		ReceivedAt: r.now(),
	}

	// Optional cryptographic authentication before any parsing happens.
	if verifier, ok := p.(provider.SignatureVerifier); ok {
		if err := verifier.VerifyWebhookSignature(wreq); err != nil {
			r.log.Warn("webhook signature verification failed",
				"provider", name, "remote_addr", req.RemoteAddr, "error", err.Error())
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_signature", "signature verification failed", "")
			return
		}
	}

	// Provider engine: normalize the gateway payload.
	evt, err := p.ParseWebhook(req.Context(), wreq)
	if err != nil {
		r.log.Warn("cannot parse webhook", "provider", name, "error", err.Error())
		httpx.WriteError(w, http.StatusBadRequest, "invalid_webhook", err.Error(), "")
		return
	}

	// Core engine: flag the transaction and notify the merchant.
	tx, err := r.engine.HandleProviderEvent(req.Context(), name, evt)
	if err != nil {
		r.log.Error("cannot apply webhook", "provider", name, "error", err.Error())
		httpx.WriteError(w, http.StatusNotFound, "transaction_not_found", err.Error(), "")
		return
	}

	r.log.Info("webhook processed",
		"provider", name, "transaction_id", tx.ID, "status", tx.Status)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"received":       true,
		"transaction_id": tx.ID,
		"status":         tx.Status,
	})
}

// Sign computes the outbound signature over "<timestamp>.<body>" using HMAC
// SHA-256, hex encoded. Merchants verify it to authenticate our callbacks.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ContextWithTimeout is a small helper so callers can bound delivery time.
func ContextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, d)
}
