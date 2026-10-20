package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
)

// TransactionRepository is the PostgreSQL implementation of
// repository.TransactionRepository.
type TransactionRepository struct {
	pool *pgxpool.Pool
}

// NewTransactionRepository wires a repository onto an existing pool.
func NewTransactionRepository(pool *pgxpool.Pool) *TransactionRepository {
	return &TransactionRepository{pool: pool}
}

// compile-time interface check.
var _ repository.TransactionRepository = (*TransactionRepository)(nil)

// Create implements repository.TransactionRepository.
func (r *TransactionRepository) Create(ctx context.Context, tx *domain.Transaction) error {
	actions, err := jsonBytes(tx.Actions)
	if err != nil {
		return fmt.Errorf("postgres: encoding actions: %w", err)
	}
	customer, err := jsonBytes(tx.Customer)
	if err != nil {
		return fmt.Errorf("postgres: encoding customer: %w", err)
	}
	items, err := jsonBytes(tx.Items)
	if err != nil {
		return fmt.Errorf("postgres: encoding items: %w", err)
	}
	metadata, err := jsonBytes(tx.Metadata)
	if err != nil {
		return fmt.Errorf("postgres: encoding metadata: %w", err)
	}

	const q = `
		INSERT INTO transactions (
			id, merchant_id, environment, reference_id, provider_name, provider_reference,
			method, channel, amount, currency, status, payment_code, actions,
			customer, items, description, metadata, failure_reason,
			expires_at, paid_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22
		)`
	_, err = r.pool.Exec(ctx, q,
		tx.ID, tx.MerchantID, tx.Environment, tx.ReferenceID, tx.ProviderName, tx.ProviderReference,
		tx.Method, tx.Channel, tx.Amount, tx.Currency, tx.Status, tx.PaymentCode, actions,
		customer, items, tx.Description, metadata, tx.FailureReason,
		tx.ExpiresAt, tx.PaidAt, tx.CreatedAt, tx.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("postgres: transaction %s/%s: %w", tx.MerchantID, tx.ReferenceID, domain.ErrConflict)
		}
		return fmt.Errorf("postgres: inserting transaction: %w", err)
	}
	return nil
}

// Update implements repository.TransactionRepository.
func (r *TransactionRepository) Update(ctx context.Context, tx *domain.Transaction) error {
	actions, err := jsonBytes(tx.Actions)
	if err != nil {
		return fmt.Errorf("postgres: encoding actions: %w", err)
	}
	const q = `
		UPDATE transactions SET
			provider_reference = $2,
			status             = $3,
			payment_code       = $4,
			actions            = $5,
			failure_reason     = $6,
			expires_at         = $7,
			paid_at            = $8,
			updated_at         = $9
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q,
		tx.ID, tx.ProviderReference, tx.Status, tx.PaymentCode, actions,
		tx.FailureReason, tx.ExpiresAt, tx.PaidAt, tx.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: updating transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: transaction %s: %w", tx.ID, domain.ErrNotFound)
	}
	return nil
}

// GetByID implements repository.TransactionRepository.
func (r *TransactionRepository) GetByID(ctx context.Context, id string) (*domain.Transaction, error) {
	const q = `SELECT ` + transactionColumns + ` FROM transactions WHERE id = $1`
	return r.queryOne(ctx, q, id)
}

// GetByReference implements repository.TransactionRepository.
func (r *TransactionRepository) GetByReference(ctx context.Context, merchantID, referenceID string) (*domain.Transaction, error) {
	const q = `SELECT ` + transactionColumns + ` FROM transactions WHERE merchant_id = $1 AND reference_id = $2`
	return r.queryOne(ctx, q, merchantID, referenceID)
}

// GetByProviderReference implements repository.TransactionRepository.
func (r *TransactionRepository) GetByProviderReference(ctx context.Context, providerName, providerReference, environment string) (*domain.Transaction, error) {
	const q = `SELECT ` + transactionColumns + `
		FROM transactions WHERE provider_name = $1 AND provider_reference = $2 AND environment = $3
		ORDER BY created_at DESC LIMIT 1`
	return r.queryOne(ctx, q, providerName, providerReference, environment)
}

// GetByProviderAndReference implements repository.TransactionRepository.
func (r *TransactionRepository) GetByProviderAndReference(ctx context.Context, providerName, referenceID, environment string) (*domain.Transaction, error) {
	const q = `SELECT ` + transactionColumns + `
		FROM transactions WHERE provider_name = $1 AND reference_id = $2 AND environment = $3
		ORDER BY created_at DESC LIMIT 1`
	return r.queryOne(ctx, q, providerName, referenceID, environment)
}

func (r *TransactionRepository) queryOne(ctx context.Context, q string, args ...any) (*domain.Transaction, error) {
	tx, err := scanTransaction(r.pool.QueryRow(ctx, q, args...))
	if err != nil {
		if isNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: querying transaction: %w", err)
	}
	return tx, nil
}

// List implements repository.TransactionRepository.
func (r *TransactionRepository) List(ctx context.Context, f repository.TransactionFilter) ([]*domain.Transaction, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.MerchantID != "" {
		add("merchant_id = $%d", f.MerchantID)
	}
	if f.Environment != "" {
		add("environment = $%d", f.Environment)
	}
	if f.ReferenceID != "" {
		add("reference_id = $%d", f.ReferenceID)
	}
	if f.ProviderName != "" {
		add("provider_name = $%d", f.ProviderName)
	}
	if f.Search != "" {
		needle := "%" + f.Search + "%"
		args = append(args, needle, needle)
		where = append(where, fmt.Sprintf(
			"(reference_id ILIKE $%d OR provider_reference ILIKE $%d)",
			len(args)-1, len(args)))
	}
	if f.Method != "" {
		add("method = $%d", f.Method)
	}
	if len(f.Statuses) > 0 {
		statuses := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			statuses[i] = string(s)
		}
		add("status = ANY($%d)", statuses)
	}
	if f.From != nil {
		add("created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("created_at <= $%d", *f.To)
	}

	q := `SELECT ` + transactionColumns + ` FROM transactions`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC"
	q += fmt.Sprintf(" LIMIT $%d", len(args)+1)
	args = append(args, clampLimit(f.Limit))
	q += fmt.Sprintf(" OFFSET $%d", len(args)+1)
	args = append(args, max(f.Offset, 0))

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing transactions: %w", err)
	}
	defer rows.Close()

	var out []*domain.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning transaction: %w", err)
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating transactions: %w", err)
	}
	return out, nil
}

func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return 50
	case limit > 500:
		return 500
	default:
		return limit
	}
}

// isUniqueViolation reports whether err is a PostgreSQL unique_violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
