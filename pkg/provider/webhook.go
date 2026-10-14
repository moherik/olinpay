package provider

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// WebhookRequest is the raw, untouched inbound callback received by the
// webhook engine from a payment gateway. The provider decides how to interpret
// it (JSON body, form-encoded body, headers, query string, ...).
type WebhookRequest struct {
	// Headers holds every HTTP header of the callback.
	Headers http.Header
	// Path is the URL path the callback arrived on, e.g. "/webhooks/winpay".
	// Gateways that follow Bank Indonesia's SNAP standard fold this path into
	// the signature string, so adapters need it to verify callbacks.
	Path string
	// Body is the raw request body. It is never modified so that signature
	// verification stays valid.
	Body []byte
	// Query holds the parsed query string, for gateways that append data to
	// the callback URL.
	Query url.Values
	// RemoteAddr is the observed client address, useful for allow-listing.
	RemoteAddr string
	// ReceivedAt is the instant the callback was received by openpay.id.
	ReceivedAt time.Time
}

// Header returns the first value of the given header, or "".
func (r *WebhookRequest) Header(key string) string {
	if r == nil || r.Headers == nil {
		return ""
	}
	return r.Headers.Get(key)
}

// JSON unmarshals the raw body into v.
func (r *WebhookRequest) JSON(v any) error {
	return json.Unmarshal(r.Body, v)
}

// WebhookEvent is the normalized result of parsing an inbound callback.
// The provider MUST resolve merchant reference and/or provider reference so the
// core engine can locate the transaction it belongs to.
type WebhookEvent struct {
	// ReferenceID is the merchant reference, if the gateway echoes it.
	ReferenceID string `json:"reference_id,omitempty"`
	// ProviderReference is the gateway transaction identifier. At least one of
	// ReferenceID / ProviderReference must be set.
	ProviderReference string `json:"provider_reference,omitempty"`
	// Status is the normalized status the callback reports.
	Status Status `json:"status"`
	// Amount is the settled amount reported by the gateway, if present.
	Amount Amount `json:"amount,omitempty"`
	// PaidAt is the settlement instant, if reported.
	PaidAt *time.Time `json:"paid_at,omitempty"`
	// EventType is an optional gateway event name (e.g. "payment.success").
	EventType string `json:"event_type,omitempty"`
	// Raw is the untouched callback payload, retained for auditing.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// Correlatable reports whether the event carries enough information for the
// core engine to locate the matching transaction.
func (e *WebhookEvent) Correlatable() bool {
	return e != nil && (e.ReferenceID != "" || e.ProviderReference != "")
}
