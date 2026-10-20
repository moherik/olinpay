// Package webhook implements the webhook engine: the inbound receiver that
// accepts callbacks from payment gateways and the outbound dispatcher that
// notifies merchant systems.
//
// Inbound flow (gateway -> openpay.id):
//
//	gateway -> POST /webhooks/{provider} (production) or
//	           /webhooks/sandbox/{provider} -> Receiver
//	        -> provider.VerifyWebhookSignature (optional)
//	        -> provider.ParseWebhook
//	        -> core.Engine.HandleProviderEvent
//
// Every attempt is recorded in the inbound-event history the admin console
// lists, whether the callback was accepted, rejected or uncorrelatable.
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
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/activity"
	"github.com/moherik/openpay.id/internal/platform/httpx"
	"github.com/moherik/openpay.id/internal/platform/id"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/pkg/provider"
)

// maxCallbackBytes caps the size of an inbound gateway callback.
const maxCallbackBytes = 2 << 20 // 2 MiB

// Receiver is the inbound webhook engine.
type Receiver struct {
	registry   *provider.Registry
	engine     *core.Engine
	events     repository.InboundEventRepository
	activities repository.TransactionActivityRepository
	log        *slog.Logger
	now        func() time.Time
	newID      func() string
}

func (r *Receiver) SetActivityRepository(repo repository.TransactionActivityRepository) {
	r.activities = repo
}

// NewReceiver builds the inbound webhook engine. The event repository is
// optional; without it no callback history is recorded.
func NewReceiver(registry *provider.Registry, engine *core.Engine, events repository.InboundEventRepository, log *slog.Logger) *Receiver {
	if log == nil {
		log = slog.Default()
	}
	return &Receiver{
		registry: registry,
		engine:   engine,
		events:   events,
		log:      log,
		now:      time.Now,
		newID:    func() string { return id.NewPrefixed("whe") },
	}
}

// Mount registers the callback route on a chi router.
//
// A single path serves every gateway; the provider is identified by the final
// path segment, e.g. POST /webhooks/mock.
func (r *Receiver) Mount(router chi.Router) {
	router.Post("/webhooks/sandbox/{provider}", r.HandleSandbox)
	router.Post("/webhooks/sandbox/{provider}/*", r.HandleSandbox)
	router.Post("/webhooks/{provider}", r.Handle)
	// Winpay and other SNAP gateways may append a product-specific callback
	// path to the provider callback base URL. Keep the provider in the first
	// path segment while preserving the complete suffix for signature checks.
	router.Post("/webhooks/{provider}/*", r.Handle)
}

// Handle processes one inbound gateway callback.
func (r *Receiver) Handle(w http.ResponseWriter, req *http.Request) {
	r.handle(w, req, domain.EnvironmentProduction)
}

// HandleSandbox processes a callback addressed to a sandbox provider instance.
func (r *Receiver) HandleSandbox(w http.ResponseWriter, req *http.Request) {
	r.handle(w, req, domain.EnvironmentSandbox)
}

func (r *Receiver) handle(w http.ResponseWriter, req *http.Request, environment string) {
	requestedName := chi.URLParam(req, "provider")
	name, p, ok := r.resolveProvider(requestedName, environment)
	startedAt := time.Now()
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "provider_not_found",
			fmt.Sprintf("no provider instance is registered for %s callback under %s", environment, requestedName), "")
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
	remoteAddr := remoteIP(req)

	wreq := &provider.WebhookRequest{
		Headers:    req.Header,
		Path:       req.URL.Path,
		Body:       body,
		Query:      req.URL.Query(),
		RemoteAddr: req.RemoteAddr,
		ReceivedAt: r.now(),
	}

	// Optional cryptographic authentication before any parsing happens.
	if verifier, ok := p.(provider.SignatureVerifier); ok {
		if err := verifier.VerifyWebhookSignature(wreq); err != nil {
			r.log.Warn("webhook signature verification failed",
				"provider", name, "remote_addr", remoteAddr, "error", err.Error())
			r.record(domain.InboundEvent{
				Provider:       name,
				Environment:    environment,
				SignatureValid: false,
				Note:           "signature verification failed: " + truncate(err.Error(), 500),
				RemoteAddr:     remoteAddr,
			})
			httpx.WriteError(w, http.StatusUnauthorized, "invalid_signature", "signature verification failed", "")
			return
		}
	}

	// Provider engine: normalize the gateway payload.
	evt, err := p.ParseWebhook(req.Context(), wreq)
	if err != nil {
		r.log.Warn("cannot parse webhook", "provider", name, "error", err.Error())
		r.record(domain.InboundEvent{
			Provider:       name,
			Environment:    environment,
			ReferenceID:    referenceFrom(evt),
			SignatureValid: true,
			Note:           "parse failed: " + truncate(err.Error(), 500),
			RemoteAddr:     remoteAddr,
		})
		httpx.WriteError(w, http.StatusBadRequest, "invalid_webhook", err.Error(), "")
		return
	}

	// Core engine: flag the transaction and notify the merchant.
	tx, err := r.engine.HandleProviderEvent(req.Context(), name, environment, evt)
	env := environment
	if tx != nil {
		env = tx.Environment
	}
	if err != nil {
		r.log.Error("cannot apply webhook", "provider", name, "error", err.Error())
		r.record(inboundFromEvent(name, env, evt, remoteAddr, false, err.Error()))
		httpx.WriteError(w, http.StatusNotFound, "transaction_not_found", err.Error(), "")
		if tx != nil {
			r.recordActivity(wreq, tx, domain.ActivityProviderWebhook, http.StatusNotFound, []byte(`{"error":"transaction not found"}`), err.Error(), nil, nil, 0, startedAt)
		}
		return
	}

	// Signature held, payload parsed, transition applied (or an idempotent
	// duplicate was accepted).
	r.record(inboundFromEvent(name, env, evt, remoteAddr, true, ""))

	r.log.Info("webhook processed",
		"provider", name, "transaction_id", tx.ID, "status", tx.Status)

	// Some gateways only stop retrying once they see their own acknowledgement
	// body; let the provider supply it when it wants to.
	if responder, ok := p.(provider.WebhookResponder); ok {
		status, ack := responder.WebhookResponse(evt)
		if status == 0 {
			status = http.StatusOK
		}
		httpx.WriteRaw(w, status, ack)
		applied := true
		signed := true
		r.recordActivity(wreq, tx, domain.ActivityProviderWebhook, status, ack, "", &signed, &applied, 0, startedAt)
		return
	}
	response := map[string]any{
		"received":       true,
		"transaction_id": tx.ID,
		"status":         tx.Status,
	}
	ack, _ := json.Marshal(response)
	signed, applied := true, true
	r.recordActivity(wreq, tx, domain.ActivityProviderWebhook, http.StatusOK, ack, "", &signed, &applied, 0, startedAt)
	httpx.WriteJSON(w, http.StatusOK, response)
}

func (r *Receiver) recordActivity(req *provider.WebhookRequest, tx *domain.Transaction, direction domain.ActivityDirection, status int, response []byte, note string, signatureValid, applied *bool, attempt int, startedAt time.Time) {
	if r.activities == nil {
		return
	}
	merchantID, environment, transactionID, reference := "", "", "", ""
	if tx != nil {
		merchantID, environment, transactionID, reference = tx.MerchantID, tx.Environment, tx.ID, tx.ReferenceID
	}
	request := &domain.TransactionActivity{ID: id.NewPrefixed("act"), TransactionID: transactionID, MerchantID: merchantID, Environment: environment, ReferenceID: reference, Direction: direction, Phase: domain.ActivityRequest, Method: http.MethodPost, URL: req.Path, Headers: map[string][]string(activity.RedactHeaders(req.Headers)), Body: activity.RedactBody(req.Body), SignatureValid: signatureValid, Applied: applied, Attempt: attempt, CreatedAt: startedAt}
	responseActivity := *request
	responseActivity.ID = id.NewPrefixed("act")
	responseActivity.Phase = domain.ActivityResponse
	responseActivity.StatusCode = status
	responseActivity.DurationMs = time.Since(startedAt).Milliseconds()
	responseActivity.Body = activity.RedactBody(response)
	responseActivity.Error = note
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	go func() {
		defer cancel()
		_ = r.activities.Create(ctx, request)
		_ = r.activities.Create(ctx, &responseActivity)
	}()
}

// inboundFromEvent builds the history row for a parsed callback.
func inboundFromEvent(providerName, env string, evt *provider.WebhookEvent, remoteAddr string, applied bool, note string) domain.InboundEvent {
	eventType := evt.EventType
	if eventType == "" {
		eventType = domain.EventName(evt.Status)
	}
	e := domain.InboundEvent{
		Provider:       providerName,
		Environment:    env,
		EventType:      eventType,
		ReferenceID:    evt.ReferenceID,
		Amount:         evt.Amount,
		Status:         string(evt.Status),
		SignatureValid: true,
		Parsed:         true,
		Applied:        applied,
		RemoteAddr:     remoteAddr,
	}
	if note != "" {
		e.Note = truncate(note, 500)
	}
	if evt.ProviderReference != "" {
		e.ProviderReference = evt.ProviderReference
	}
	return e
}

// referenceFrom is a best-effort reference for parse failures; the event is
// usually nil there, so the row simply carries the provider name.
func referenceFrom(evt *provider.WebhookEvent) string {
	if evt == nil {
		return ""
	}
	return evt.ReferenceID
}

// record persists one history row, never failing the callback path because of
// logging trouble.
func (r *Receiver) record(e domain.InboundEvent) {
	if r.events == nil {
		return
	}
	e.ID = r.newID()
	if e.ReceivedAt.IsZero() {
		e.ReceivedAt = r.now()
	}
	if err := r.events.Create(context.Background(), &e); err != nil {
		r.log.Error("persisting inbound event failed", "provider", e.Provider, "error", err.Error())
	}
}

func (r *Receiver) resolveProvider(requestedName, environment string) (string, provider.Provider, bool) {
	instanceName := domain.InstanceName(requestedName, environment)
	if p, ok := r.registry.Get(instanceName); ok {
		return p.Name(), p, true
	}
	// Provider IDs are adapter names, never environment-suffixed instance names.
	if strings.Contains(requestedName, ".") {
		return "", nil, false
	}
	// Unsuffixed providers are legacy environment-variable instances. They
	// serve both callback paths when no database-backed instance exists.
	p, ok := r.registry.Get(requestedName)
	if ok {
		return p.Name(), p, true
	}
	return requestedName, nil, false
}

// remoteIP strips the port from RemoteAddr for the audit trail.
func remoteIP(req *http.Request) string {
	addr := req.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i > 0 && !strings.Contains(addr[i:], "]") {
		return addr[:i]
	}
	return addr
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
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
