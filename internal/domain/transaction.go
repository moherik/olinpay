package domain

import (
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// Transaction is the core engine's persisted view of a payment attempt. It is
// the single source of truth for status, and it is what the API layer returns
// and what is serialized into outbound merchant webhooks.
//
// Transaction deliberately reuses the unified [provider.Status]/[provider.Method]
// vocabulary so that no translation is needed between the provider SDK, the
// core engine, and API consumers.
type Transaction struct {
	ID                string                   `json:"id"`
	MerchantID        string                   `json:"merchant_id"`
	Environment       string                   `json:"environment"`
	ReferenceID       string                   `json:"reference_id"`
	ProviderName      string                   `json:"provider"`
	ProviderReference string                   `json:"provider_reference,omitempty"`
	Method            provider.Method          `json:"method"`
	Channel           string                   `json:"channel,omitempty"`
	Amount            provider.Amount          `json:"amount"`
	Currency          provider.Currency        `json:"currency"`
	Status            provider.Status          `json:"status"`
	PaymentCode       string                   `json:"payment_code,omitempty"`
	Actions           []provider.PaymentAction `json:"actions,omitempty"`
	Customer          provider.Customer        `json:"customer"`
	Items             []provider.Item          `json:"items,omitempty"`
	Description       string                   `json:"description,omitempty"`
	Metadata          map[string]string        `json:"metadata,omitempty"`
	FailureReason     string                   `json:"failure_reason,omitempty"`
	ExpiresAt         *time.Time               `json:"expires_at,omitempty"`
	PaidAt            *time.Time               `json:"paid_at,omitempty"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// CanTransitionTo reports whether moving from the current status to next is a
// legal lifecycle transition. It guards against out-of-order webhooks that
// would otherwise resurrect a settled transaction.
func (t *Transaction) CanTransitionTo(next provider.Status) bool {
	cur := t.Status
	if cur == next {
		// Idempotent repeat: allowed only while nothing changes.
		return true
	}
	switch cur {
	case provider.StatusUnknown:
		return true
	case provider.StatusPending:
		switch next {
		case provider.StatusPaid, provider.StatusExpired, provider.StatusCancelled, provider.StatusFailed:
			return true
		}
		return false
	case provider.StatusPaid:
		return next == provider.StatusRefunded
	default:
		// expired / cancelled / failed / refunded are terminal.
		return false
	}
}

// IsTerminal reports whether the transaction has reached a final status.
func (t *Transaction) IsTerminal() bool { return t.Status.IsFinal() }

// ApplyStatus transitions the transaction to next, updating the derived
// timestamps. It returns false when the transition is illegal, leaving the
// transaction untouched.
func (t *Transaction) ApplyStatus(next provider.Status, paidAt *time.Time, failure string) bool {
	if !t.CanTransitionTo(next) {
		return false
	}
	t.Status = next
	t.UpdatedAt = time.Now()
	switch next {
	case provider.StatusPaid:
		if paidAt != nil {
			t.PaidAt = paidAt
		} else if t.PaidAt == nil {
			now := time.Now()
			t.PaidAt = &now
		}
		t.FailureReason = ""
	case provider.StatusFailed:
		t.FailureReason = failure
	}
	return true
}

// EventType maps a status onto the outbound webhook event name sent to the
// merchant system (e.g. "payment.paid").
func (t *Transaction) EventType() string {
	return EventName(t.Status)
}

// EventName maps a unified status to its outbound webhook event name.
func EventName(s provider.Status) string {
	switch s {
	case provider.StatusPending:
		return "payment.pending"
	case provider.StatusPaid:
		return "payment.paid"
	case provider.StatusExpired:
		return "payment.expired"
	case provider.StatusCancelled:
		return "payment.cancelled"
	case provider.StatusFailed:
		return "payment.failed"
	case provider.StatusRefunded:
		return "payment.refunded"
	default:
		return "payment.unknown"
	}
}
