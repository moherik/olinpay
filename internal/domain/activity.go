package domain

import "time"

type ActivityDirection string

const (
	ActivityMerchantRequest  ActivityDirection = "merchant_request"
	ActivityMerchantResponse ActivityDirection = "merchant_response"
	ActivityProviderRequest  ActivityDirection = "provider_request"
	ActivityProviderResponse ActivityDirection = "provider_response"
	ActivityProviderWebhook  ActivityDirection = "provider_webhook"
	ActivityMerchantWebhook  ActivityDirection = "merchant_webhook"
)

type ActivityPhase string

const (
	ActivityRequest  ActivityPhase = "request"
	ActivityResponse ActivityPhase = "response"
	ActivityEvent    ActivityPhase = "event"
)

// TransactionActivity is one redacted, bounded exchange in a transaction's
// lifecycle. TransactionID can be empty for provider calls made before the
// transaction row is persisted; ReferenceID keeps those rows correlatable.
type TransactionActivity struct {
	ID             string              `json:"id"`
	TransactionID  string              `json:"transaction_id,omitempty"`
	MerchantID     string              `json:"merchant_id"`
	Environment    string              `json:"environment,omitempty"`
	ReferenceID    string              `json:"reference_id,omitempty"`
	Direction      ActivityDirection   `json:"direction"`
	Phase          ActivityPhase       `json:"phase"`
	Provider       string              `json:"provider,omitempty"`
	EventType      string              `json:"event_type,omitempty"`
	Method         string              `json:"method,omitempty"`
	URL            string              `json:"url,omitempty"`
	StatusCode     int                 `json:"status_code,omitempty"`
	DurationMs     int64               `json:"duration_ms,omitempty"`
	Attempt        int                 `json:"attempt,omitempty"`
	SignatureValid *bool               `json:"signature_valid,omitempty"`
	Applied        *bool               `json:"applied,omitempty"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Body           string              `json:"body,omitempty"`
	Error          string              `json:"error,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
}
