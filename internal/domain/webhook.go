package domain

import (
	"time"
)

// DeliveryStatus is the state of an outbound merchant webhook.
type DeliveryStatus string

const (
	// DeliveryPending means the webhook is queued and not yet delivered.
	DeliveryPending DeliveryStatus = "pending"
	// DeliveryDelivered means the merchant acknowledged it with a 2xx.
	DeliveryDelivered DeliveryStatus = "delivered"
	// DeliveryFailed means all attempts were exhausted.
	DeliveryFailed DeliveryStatus = "failed"
)

// WebhookDelivery is a queued outbound notification to the merchant system.
// It is persisted before delivery so that a crash never loses an event, and it
// is retried with backoff until acknowledged or exhausted.
type WebhookDelivery struct {
	ID            string         `json:"id"`
	TransactionID string         `json:"transaction_id"`
	MerchantID    string         `json:"merchant_id"`
	EventType     string         `json:"event_type"`
	URL           string         `json:"url"`
	Payload       []byte         `json:"payload"`
	Status        DeliveryStatus `json:"status"`
	Attempts      int            `json:"attempts"`
	MaxAttempts   int            `json:"max_attempts"`
	LastError     string         `json:"last_error,omitempty"`
	LastAttemptAt *time.Time     `json:"last_attempt_at,omitempty"`
	NextAttemptAt *time.Time     `json:"next_attempt_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// WebhookMessage is the JSON envelope POSTed to a merchant's callback URL.
// It is stable and versioned so integrators can rely on it.
type WebhookMessage struct {
	// ID is the delivery identifier; it doubles as an idempotency key for the
	// merchant (repeat deliveries share the same ID).
	ID string `json:"id"`
	// Event is the event name, e.g. "payment.paid".
	Event string `json:"event"`
	// CreatedAt is the instant the event was produced.
	CreatedAt time.Time `json:"created_at"`
	// Data is the full transaction snapshot at the time of the event.
	Data *Transaction `json:"data"`
}
