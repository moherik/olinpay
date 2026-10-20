package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/pkg/provider"
)

// listDeliveries serves GET /api/deliveries?env=&status=&limit=&offset=.
func (s *Server) listDeliveries(w http.ResponseWriter, r *http.Request) {
	env, ok := resolveEnvironment(w, r.URL.Query().Get("env"))
	if !ok {
		return
	}
	f := repository.DeliveryFilter{
		Environment: env,
		Limit:       parseInt(r.URL.Query().Get("limit"), 50),
		Offset:      parseInt(r.URL.Query().Get("offset"), 0),
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if status := domain.DeliveryStatus(strings.TrimSpace(r.URL.Query().Get("status"))); status != "" {
		f.Status = status
	}

	dels, err := s.deliveries.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot list webhook deliveries")
		return
	}
	writeData(w, http.StatusOK, dels, paginationMeta(f.Limit, f.Offset, len(dels)))
}

// retryDelivery serves POST /api/deliveries/{id}/retry. Only terminal
// failures (or delivered rows for a deliberate re-push) are requeued; a
// delivery that is currently pending is left to the dispatcher.
func (s *Server) retryDelivery(w http.ResponseWriter, r *http.Request) {
	d, err := s.deliveries.GetByID(r.Context(), chiURLParam(r, "id"))
	if err != nil {
		notFound(w, "webhook delivery")
		return
	}
	if d.Status == domain.DeliveryPending {
		writeError(w, http.StatusConflict, "already_pending", "delivery is already pending")
		return
	}

	now := s.now()
	d.Status = domain.DeliveryPending
	d.Attempts = 0
	d.LastError = ""
	d.NextAttemptAt = &now
	d.UpdatedAt = now
	if err := s.deliveries.Update(r.Context(), d); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot requeue delivery")
		return
	}

	if user, ok := userFromContext(r.Context()); ok {
		s.auditWrite(r, user, "webhook.retry", d.ID)
	}
	writeData(w, http.StatusOK, d, map[string]any{"requeued": true})
}

// listInbound serves GET /api/inbound?env=&limit=&offset=. Rows without a
// resolved environment (uncorrelated callbacks) stay visible under every
// environment filter — the console should never hide unexplained traffic.
func (s *Server) listInbound(w http.ResponseWriter, r *http.Request) {
	env, ok := resolveEnvironment(w, r.URL.Query().Get("env"))
	if !ok {
		return
	}
	f := repository.InboundFilter{
		Environment: env,
		Limit:       parseInt(r.URL.Query().Get("limit"), 50),
		Offset:      parseInt(r.URL.Query().Get("offset"), 0),
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if txID := strings.TrimSpace(r.URL.Query().Get("transaction_id")); txID != "" {
		f.TransactionID = txID
	}

	events, err := s.inbound.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot list inbound events")
		return
	}
	writeData(w, http.StatusOK, events, paginationMeta(f.Limit, f.Offset, len(events)))
}

// reprocessInbound serves POST /api/inbound/{id}/reprocess. It replays the
// stored normalized event through the core engine — the same path a live
// callback takes, so duplicate and out-of-order guards apply unchanged.
// Only callbacks whose signature verified and that parsed cleanly are
// reprocessable; reprocessing a rejected callback would bypass the provider's
// own verification, which is exactly what reconciliation must not do.
func (s *Server) reprocessInbound(w http.ResponseWriter, r *http.Request) {
	e, err := s.inbound.GetByID(r.Context(), chiURLParam(r, "id"))
	if err != nil {
		notFound(w, "inbound event")
		return
	}
	if !e.SignatureValid || !e.Parsed {
		writeError(w, http.StatusUnprocessableEntity, "not_reprocessable",
			"only verified and parsed callbacks can be reprocessed")
		return
	}
	if e.ReferenceID == "" && e.ProviderReference == "" {
		writeError(w, http.StatusUnprocessableEntity, "not_reprocessable",
			"event carries no reference and cannot be correlated")
		return
	}

	evt := &provider.WebhookEvent{
		ReferenceID:       e.ReferenceID,
		ProviderReference: e.ProviderReference,
		Status:            provider.Status(e.Status),
		Amount:            provider.Amount(e.Amount),
		EventType:         e.EventType,
	}

	tx, err := s.engine.HandleProviderEvent(r.Context(), e.Provider, e.Environment, evt)
	if err != nil {
		s.writeEngineError(w, err, "reprocessing failed")
		return
	}

	e.Applied = true
	if tx != nil {
		e.TransactionID = tx.ID
		e.Environment = tx.Environment
	}
	e.Note = "reprocessed at " + s.now().UTC().Format(time.RFC3339)
	if err := s.inbound.Update(r.Context(), e); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot update inbound event")
		return
	}

	if user, ok := userFromContext(r.Context()); ok {
		s.auditWrite(r, user, "inbound.reprocess", e.ID)
	}

	// The event row mirrors history; the refreshed transaction is what the
	// console shows afterwards.
	detail, _ := s.transactions.GetByID(context.Background(), e.TransactionID)
	payload := map[string]any{"event": e}
	if detail != nil {
		payload["transaction"] = detail
	}
	writeData(w, http.StatusOK, payload, map[string]any{"reprocessed": true})
}
