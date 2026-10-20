package admin

import (
	"net/http"
	"strings"

	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/pkg/provider"
)

// transactionFilter builds a repository filter from query parameters.
func transactionFilter(env string, q map[string]string) repository.TransactionFilter {
	f := repository.TransactionFilter{
		Environment: env,
		Limit:       parseInt(q["limit"], 50),
		Offset:      parseInt(q["offset"], 0),
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	f.MerchantID = strings.TrimSpace(q["merchant_id"])
	f.ProviderName = strings.TrimSpace(q["provider"])
	f.Search = strings.TrimSpace(q["q"])
	if st := strings.TrimSpace(q["status"]); st != "" {
		value := provider.Status(st)
		if value.IsValid() {
			f.Statuses = []provider.Status{value}
		}
	}
	return f
}

// listTransactions serves GET /api/transactions.
//
// The reference_id filter is exact match on purpose: the console search field
// deliberately treats references as identifiers, never as fuzzy text.
func (s *Server) listTransactions(w http.ResponseWriter, r *http.Request) {
	env, ok := resolveEnvironment(w, r.URL.Query().Get("env"))
	if !ok {
		return
	}
	q := qmap(r)
	f := transactionFilter(env, q)

	txs, err := s.transactions.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot list transactions")
		return
	}

	data := make([]*domain.Transaction, 0, len(txs))
	for _, tx := range txs {
		data = append(data, tx)
	}
	writeData(w, http.StatusOK, data, paginationMeta(f.Limit, f.Offset, len(data)))
}

// getTransaction serves GET /api/transactions/{id}. When ?sync=1, a live
// gateway inquiry refreshes the status before responding (only when the
// provider supports inquiry and the transaction is still non-terminal).
func (s *Server) getTransaction(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	tx, err := s.transactions.GetByID(r.Context(), id)
	if err != nil {
		notFound(w, "transaction")
		return
	}

	if r.URL.Query().Get("sync") == "1" {
		refreshed, err := s.engine.Inquiry(r.Context(), core.InquiryQuery{TransactionID: id, Sync: true})
		if err == nil {
			tx = refreshed
		}
		// A failed gateway sync is not an error for the page: the stored
		// state is still shown, with the refresh outcome surfaced below.
	}

	meta := s.transactionMeta(tx)
	if r.URL.Query().Get("sync") == "1" {
		meta["synced"] = err == nil
		if err != nil {
			meta["sync_error"] = truncateText(err.Error(), 200)
		}
	}
	writeData(w, http.StatusOK, tx, meta)
}

func (s *Server) listTransactionActivities(w http.ResponseWriter, r *http.Request) {
	id := chiURLParam(r, "id")
	tx, err := s.transactions.GetByID(r.Context(), id)
	if err != nil {
		notFound(w, "transaction")
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"), 200)
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if s.activities == nil {
		writeData(w, http.StatusOK, []*domain.TransactionActivity{}, map[string]any{"count": 0})
		return
	}
	items, err := s.activities.List(r.Context(), repository.ActivityFilter{
		TransactionID: id,
		MerchantID:    tx.MerchantID,
		Environment:   tx.Environment,
		ReferenceID:   tx.ReferenceID,
		Limit:         limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot list transaction activity")
		return
	}
	writeData(w, http.StatusOK, items, map[string]any{"count": len(items)})
}

// cancelTransaction serves POST /api/transactions/{id}/cancel {reason}.
// The core engine owns the status transition: this handler only authorizes it
// (the route is operator and above) and records the audit entry.
func (s *Server) cancelTransaction(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil && !isEOF(err) {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	tx, err := s.engine.Cancel(r.Context(), core.CancelCommand{
		TransactionID: chiURLParam(r, "id"),
		Reason:        strings.TrimSpace(body.Reason),
	})
	if err != nil {
		s.writeEngineError(w, err, "cannot cancel transaction")
		return
	}

	if user, ok := userFromContext(r.Context()); ok {
		s.auditWrite(r, user, "transaction.cancel", tx.ID)
	}
	writeData(w, http.StatusOK, tx, s.transactionMeta(tx))
}

// transactionMeta reports what the console may offer on this transaction.
func (s *Server) transactionMeta(tx *domain.Transaction) map[string]any {
	meta := map[string]any{
		"can_cancel": false,
		"can_sync":   false,
	}
	p, ok := s.registry.Get(domain.InstanceName(tx.ProviderName, tx.Environment))
	if !ok {
		p, ok = s.providerGet(tx.ProviderName)
	}
	if ok {
		caps := p.Capabilities()
		meta["can_sync"] = caps.SupportsInquiry && tx.ProviderReference != "" && !tx.IsTerminal()
		meta["can_cancel"] = caps.SupportsCancel && !tx.IsTerminal()
	}
	return meta
}
