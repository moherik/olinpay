package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/httpx"
	"github.com/moherik/openpay.id/internal/platform/id"
	"github.com/moherik/openpay.id/pkg/provider"
)

// envelope mirrors the core API's success envelope so the console talks to one
// response shape.
type envelope struct {
	Data any            `json:"data"`
	Meta map[string]any `json:"meta,omitempty"`
}

func writeData(w http.ResponseWriter, status int, data any, meta map[string]any) {
	// A nil slice marshals to null, which a typed consumer cannot iterate.
	// Emit an empty array instead so list endpoints always answer with [].
	if v := reflect.ValueOf(data); v.IsValid() && v.Kind() == reflect.Slice && v.IsNil() {
		data = reflect.MakeSlice(v.Type(), 0, 0).Interface()
	}
	httpx.WriteJSON(w, status, envelope{Data: data, Meta: meta})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	httpx.WriteError(w, status, code, message, "")
}

func unauthorized(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
}

func forbidden(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "forbidden", "your role does not allow this action")
}

func notFound(w http.ResponseWriter, what string) {
	writeError(w, http.StatusNotFound, "not_found", what+" not found")
}

func invalidField(w http.ResponseWriter, field, message string) {
	httpx.WriteError(w, http.StatusBadRequest, "validation_error", message, field)
}

// decodeJSON parses a JSON body with the shared bounded decoder.
func decodeJSON(r *http.Request, v any) error {
	return httpx.DecodeJSON(r, v, 0)
}

// isEOF reports whether the request body was empty (legal for endpoints whose
// entire body is optional).
func isEOF(err error) bool {
	return errors.Is(err, io.EOF)
}

// clientIP strips the port from RemoteAddr for audit rows.
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i > 0 && !strings.Contains(addr[i:], "]") {
		return addr[:i]
	}
	return addr
}

func newPrefixedID(prefix string) string { return id.NewPrefixed(prefix) }

// qmap flattens the query parameters for the filter builders.
func qmap(r *http.Request) map[string]string {
	out := map[string]string{}
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			out[key] = values[0]
		}
	}
	return out
}

// chiURLParam reads a chi route parameter.
func chiURLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

// parseInt parses a query integer with a fallback default.
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

// paginationMeta reports the single-page shape used by all list endpoints:
// has_more lets the console stop paginating without a COUNT(*) round trip.
func paginationMeta(limit, offset, count int) map[string]any {
	return map[string]any{
		"limit":    limit,
		"offset":   offset,
		"count":    count,
		"has_more": count == limit,
	}
}

// resolveEnvironment normalizes the ?env= parameter. The empty string means
// "all environments"; anything else must be a known environment.
func resolveEnvironment(w http.ResponseWriter, raw string) (string, bool) {
	env := strings.TrimSpace(raw)
	switch env {
	case "":
		return "", true
	case domain.EnvironmentSandbox, domain.EnvironmentProduction:
		return env, true
	default:
		invalidField(w, "env", "environment must be sandbox or production")
		return "", false
	}
}

// auditWrite appends an audit row for a state-changing admin action. Audit
// failures are logged but never fail the action itself: the operation already
// happened.
func (s *Server) auditWrite(r *http.Request, user *domain.AdminUser, action, target string) {
	if s.audit == nil {
		return
	}
	entry := &domain.AuditEntry{
		ID:     s.newID("aud"),
		Actor:  user.Email,
		Action: action,
		Target: truncateText(target, 200),
		IP:     clientIP(r),
	}
	entry.At = s.now()
	if err := s.audit.Create(r.Context(), entry); err != nil {
		s.log.Error("audit write failed", "action", action, "error", err.Error())
	}
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// mapConfig decodes a JSON object of string values (provider configuration).
func mapConfig(raw json.RawMessage) (map[string]string, error) {
	cfg := map[string]string{}
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config must be an object of strings")
	}
	return cfg, nil
}

// providerGet resolves a provider instance by name from the server registry.
func (s *Server) providerGet(name string) (provider.Provider, bool) {
	return s.registry.Get(name)
}

// writeEngineError maps engine/domain/provider errors onto HTTP statuses the
// same way the merchant API does, keeping one error contract for the console.
func (s *Server) writeEngineError(w http.ResponseWriter, err error, fallback string) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		httpx.WriteError(w, http.StatusBadRequest, "validation_error", ve.Message, ve.Field)
		return
	}

	status, code := engineStatus(err)
	msg := fallback
	if status == http.StatusInternalServerError {
		s.log.Error("admin: engine error", "error", err.Error())
		msg = "internal error"
	}
	httpx.WriteError(w, status, code, msg, "")
}

// engineStatus maps the sentinel errors used across the core engine.
func engineStatus(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, "validation_error"
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrUnprocessable):
		return http.StatusUnprocessableEntity, "unprocessable"
	case errors.Is(err, domain.ErrNoRoute):
		return http.StatusConflict, "no_route"
	case errors.Is(err, provider.ErrUnsupportedOperation):
		return http.StatusBadRequest, "unsupported_operation"
	case errors.Is(err, provider.ErrInvalidRequest):
		return http.StatusBadRequest, "validation_error"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}
