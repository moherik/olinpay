package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
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

// createPayment handles POST /v1/payments.
func (s *Server) createPayment(w http.ResponseWriter, r *http.Request) {
	merchant, ok := merchantFromContext(r.Context())
	if !ok {
		fail(w, r, domain.ErrUnauthorized, "")
		return
	}

	var body createPaymentRequest
	rawRequest, readErr := io.ReadAll(io.LimitReader(r.Body, httpx.MaxBodyBytes+1))
	if readErr != nil || len(rawRequest) > httpx.MaxBodyBytes {
		fail(w, r, domain.Invalid("body", "request body is too large"), "")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(rawRequest))
	if err := httpx.DecodeJSON(r, &body, 0); err != nil {
		fail(w, r, domain.Invalid("body", err.Error()), "")
		return
	}

	items := make([]provider.Item, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, it.toProvider())
	}

	cmd := core.CreatePaymentCommand{
		MerchantID:   merchant.ID,
		Environment:  environmentFromContext(r.Context()),
		ReferenceID:  body.ReferenceID,
		Amount:       body.Amount,
		Currency:     provider.Currency(strings.ToUpper(body.Currency)),
		Method:       provider.Method(body.Method),
		Channel:      body.Channel,
		ProviderName: body.Provider,
		Customer:     body.Customer.toProvider(),
		Items:        items,
		Description:  body.Description,
		Expiry:       time.Duration(body.ExpirySeconds) * time.Second,
		Metadata:     body.Metadata,
	}
	startedAt := time.Now()

	tx, err := s.engine.CreatePayment(r.Context(), cmd)
	if err != nil {
		fail(w, r, err, "")
		return
	}
	s.recordMerchantActivity(r, domain.ActivityMerchantRequest, merchant.ID, cmd.Environment, body.ReferenceID, tx.ID, rawRequest, 0, startedAt)
	writeData(w, http.StatusCreated, tx, nil)
	responseBody, _ := json.Marshal(map[string]any{"data": tx})
	s.recordMerchantActivity(r, domain.ActivityMerchantResponse, tx.MerchantID, tx.Environment, tx.ReferenceID, tx.ID, responseBody, http.StatusCreated, time.Now())
}

func (s *Server) recordMerchantActivity(r *http.Request, direction domain.ActivityDirection, merchantID, environment, referenceID, transactionID string, body []byte, status int, createdAt time.Time) {
	if s.activities == nil {
		return
	}
	activityRow := domain.TransactionActivity{
		MerchantID: merchantID, Environment: environment, ReferenceID: referenceID,
		TransactionID: transactionID, Direction: direction, Phase: domain.ActivityRequest,
		Method: r.Method, URL: r.URL.String(), CreatedAt: createdAt,
		Headers: activity.RedactHeaders(r.Header),
	}
	activityRow.ID = id.NewPrefixed("act")
	activityRow.Phase = domain.ActivityRequest
	if direction == domain.ActivityMerchantResponse {
		activityRow.Phase = domain.ActivityResponse
	}
	activityRow.StatusCode = status
	activityRow.Body = activity.RedactBody(body)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.activities.Create(ctx, &activityRow)
}

// getPayment handles GET /v1/payments/{id}. Use ?sync=true to refresh from the
// gateway.
func (s *Server) getPayment(w http.ResponseWriter, r *http.Request) {
	merchant, ok := merchantFromContext(r.Context())
	if !ok {
		fail(w, r, domain.ErrUnauthorized, "")
		return
	}

	tx, err := s.engine.Inquiry(r.Context(), core.InquiryQuery{
		MerchantID:    merchant.ID,
		TransactionID: chi.URLParam(r, "id"),
		Sync:          parseBool(r.URL.Query().Get("sync")),
	})
	if err != nil {
		fail(w, r, err, "")
		return
	}
	writeData(w, http.StatusOK, tx, nil)
}

// listPayments handles GET /v1/payments.
func (s *Server) listPayments(w http.ResponseWriter, r *http.Request) {
	merchant, ok := merchantFromContext(r.Context())
	if !ok {
		fail(w, r, domain.ErrUnauthorized, "")
		return
	}
	q := r.URL.Query()

	filter := repository.TransactionFilter{
		MerchantID:   merchant.ID,
		ReferenceID:  q.Get("reference_id"),
		ProviderName: q.Get("provider"),
		Method:       provider.Method(q.Get("method")),
		Limit:        parseInt(q.Get("limit"), 50),
		Offset:       parseInt(q.Get("offset"), 0),
	}
	if statuses := q["status"]; len(statuses) > 0 {
		for _, raw := range statuses {
			for _, part := range strings.Split(raw, ",") {
				if part = strings.TrimSpace(part); part != "" {
					filter.Statuses = append(filter.Statuses, provider.Status(part))
				}
			}
		}
	}
	if from := parseTime(q.Get("from")); from != nil {
		filter.From = from
	}
	if to := parseTime(q.Get("to")); to != nil {
		filter.To = to
	}

	txs, err := s.engine.List(r.Context(), filter)
	if err != nil {
		fail(w, r, err, "")
		return
	}
	writeData(w, http.StatusOK, txs, map[string]any{
		"count":  len(txs),
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

// cancelPayment handles POST /v1/payments/{id}/cancel.
func (s *Server) cancelPayment(w http.ResponseWriter, r *http.Request) {
	merchant, ok := merchantFromContext(r.Context())
	if !ok {
		fail(w, r, domain.ErrUnauthorized, "")
		return
	}

	var body cancelPaymentRequest
	// A body is optional: cancelling does not require one.
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &body, 0); err != nil {
			fail(w, r, domain.Invalid("body", err.Error()), "")
			return
		}
	}

	tx, err := s.engine.Cancel(r.Context(), core.CancelCommand{
		MerchantID:    merchant.ID,
		TransactionID: chi.URLParam(r, "id"),
		Reason:        body.Reason,
	})
	if err != nil {
		fail(w, r, err, "")
		return
	}
	writeData(w, http.StatusOK, tx, nil)
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

func parseInt(raw string, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func parseTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t
	}
	return nil
}
