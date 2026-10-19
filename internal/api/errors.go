package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/httpx"
	"github.com/moherik/openpay.id/pkg/provider"
)

// envelope is the success response wrapper used by every endpoint.
type envelope struct {
	Data any            `json:"data"`
	Meta map[string]any `json:"meta,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	httpx.WriteJSON(w, status, data)
}

func writeData(w http.ResponseWriter, status int, data any, meta map[string]any) {
	httpx.WriteJSON(w, status, envelope{Data: data, Meta: meta})
}

// fail maps a domain/provider error onto an HTTP status code and the standard
// error envelope. It never leaks internal details for unexpected errors.
func fail(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	status, code := statusFor(err)

	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		httpx.WriteError(w, status, code, ve.Message, ve.Field)
		return
	}

	// A routing respond/reject node answers with its own configured status,
	// code and message instead of the standard error mapping. The stored
	// status must be a 4xx/5xx (save-time validation enforces it); a stray
	// stored value falls back to 422 so a reject can never ship as 2xx/3xx.
	var rejected *domain.RejectedError
	if errors.As(err, &rejected) {
		respStatus := rejected.Status
		if respStatus < http.StatusBadRequest || respStatus > 599 {
			respStatus = http.StatusUnprocessableEntity
		}
		respCode := rejected.Code
		if respCode == "" {
			respCode = "routing_rejected"
		}
		respMessage := rejected.Message
		if respMessage == "" {
			respMessage = domain.ErrRejected.Error()
		}
		httpx.WriteError(w, respStatus, respCode, respMessage, "")
		return
	}

	msg := fallback
	if msg == "" {
		msg = messageFor(err)
	}
	if status >= http.StatusInternalServerError {
		// Log the real cause, return a generic message.
		slog.Error("api: internal error",
			"request_id", middleware.GetReqID(r.Context()),
			"error", err.Error())
		httpx.WriteError(w, status, code, "internal server error", "")
		return
	}
	httpx.WriteError(w, status, code, msg, "")
}

func statusFor(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, "validation_error"
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrNoRoute):
		return http.StatusUnprocessableEntity, "no_route"
	case errors.Is(err, domain.ErrUnprocessable):
		return http.StatusUnprocessableEntity, "unprocessable"
	case errors.Is(err, provider.ErrUnsupportedOperation):
		return http.StatusUnprocessableEntity, "unsupported_operation"
	case errors.Is(err, provider.ErrInvalidRequest):
		return http.StatusBadRequest, "provider_invalid_request"
	case errors.Is(err, provider.ErrDuplicateReference):
		return http.StatusConflict, "duplicate_reference"
	case errors.Is(err, provider.ErrNotFound):
		return http.StatusNotFound, "provider_not_found"
	case errors.Is(err, provider.ErrUnauthorized):
		return http.StatusBadGateway, "provider_unauthorized"
	case errors.Is(err, provider.ErrGateway):
		return http.StatusBadGateway, "provider_error"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

func messageFor(err error) string {
	if err == nil {
		return ""
	}
	// Validation errors are handled by the caller; other 4xx errors expose the
	// message because it originates from our own checks or the gateway.
	return err.Error()
}

// requestLogger logs one structured line per request.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		level := slog.LevelInfo
		if ww.Status() >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if ww.Status() >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		s.log.Log(r.Context(), level, "http request",
			"request_id", middleware.GetReqID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}
