package postgres

import (
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/moherik/openpay.id/internal/domain"
)

// scannable is satisfied by both pgx.Row and pgx.Rows, letting one scan helper
// serve single-row and list queries.
type scannable interface {
	Scan(dest ...any) error
}

// jsonBytes marshals v for a jsonb column. Nil values (including nil slices and
// maps, which marshal to JSON "null") are stored as SQL NULL.
func jsonBytes(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" {
		return nil, nil
	}
	return b, nil
}

// decodeJSON unmarshals raw jsonb bytes into v when present.
func decodeJSON(raw []byte, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

const transactionColumns = `id, merchant_id, reference_id, provider_name, provider_reference,
	method, channel, amount, currency, status, payment_code, actions, customer, items,
	description, metadata, failure_reason, expires_at, paid_at, created_at, updated_at`

func scanTransaction(s scannable) (*domain.Transaction, error) {
	var (
		tx       domain.Transaction
		actions  []byte
		customer []byte
		items    []byte
		metadata []byte
	)
	err := s.Scan(
		&tx.ID, &tx.MerchantID, &tx.ReferenceID, &tx.ProviderName, &tx.ProviderReference,
		&tx.Method, &tx.Channel, &tx.Amount, &tx.Currency, &tx.Status, &tx.PaymentCode,
		&actions, &customer, &items, &tx.Description, &metadata, &tx.FailureReason,
		&tx.ExpiresAt, &tx.PaidAt, &tx.CreatedAt, &tx.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := decodeJSON(actions, &tx.Actions); err != nil {
		return nil, err
	}
	if err := decodeJSON(customer, &tx.Customer); err != nil {
		return nil, err
	}
	if err := decodeJSON(items, &tx.Items); err != nil {
		return nil, err
	}
	if err := decodeJSON(metadata, &tx.Metadata); err != nil {
		return nil, err
	}
	return &tx, nil
}

const merchantColumns = `id, name, api_key_hash, api_key_prefix, callback_url, webhook_secret,
	routing, status, created_at, updated_at`

func scanMerchant(s scannable) (*domain.Merchant, error) {
	var (
		m       domain.Merchant
		routing []byte
	)
	err := s.Scan(
		&m.ID, &m.Name, &m.APIKeyHash, &m.APIKeyPrefix, &m.CallbackURL, &m.WebhookSecret,
		&routing, &m.Status, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := decodeJSON(routing, &m.Routing); err != nil {
		return nil, err
	}
	return &m, nil
}

const deliveryColumns = `id, transaction_id, merchant_id, event_type, url, payload, status,
	attempts, max_attempts, last_error, last_attempt_at, next_attempt_at, created_at, updated_at`

func scanDelivery(s scannable) (*domain.WebhookDelivery, error) {
	var (
		d       domain.WebhookDelivery
		payload []byte
	)
	err := s.Scan(
		&d.ID, &d.TransactionID, &d.MerchantID, &d.EventType, &d.URL, &payload, &d.Status,
		&d.Attempts, &d.MaxAttempts, &d.LastError, &d.LastAttemptAt, &d.NextAttemptAt,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	d.Payload = payload
	return &d, nil
}

// isNoRows reports whether err is the pgx "no rows" sentinel.
func isNoRows(err error) bool { return err == pgx.ErrNoRows }
