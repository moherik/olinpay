// Package httpx holds small HTTP helpers shared by the API and webhook layers.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// WriteJSON writes v as JSON with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// ErrorBody is the standard error envelope returned to API callers.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes a single error.
type ErrorDetail struct {
	// Code is a stable, machine readable identifier (e.g. "validation_error").
	Code string `json:"code"`
	// Message is a human readable description.
	Message string `json:"message"`
	// Field is set for validation errors.
	Field string `json:"field,omitempty"`
}

// WriteError writes the standard error envelope.
func WriteError(w http.ResponseWriter, status int, code, message, field string) {
	WriteJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message, Field: field}})
}

// MaxBodyBytes is the default cap applied by [DecodeJSON].
const MaxBodyBytes = 1 << 20 // 1 MiB

// DecodeJSON decodes the request body into dst, rejecting unknown fields and
// bodies larger than maxBytes. It returns a descriptive error suitable for a
// 400 response.
func DecodeJSON(r *http.Request, dst any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = MaxBodyBytes
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON body: unexpected trailing data")
	}
	return nil
}
