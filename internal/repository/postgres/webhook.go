package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
)

// WebhookRepository is the PostgreSQL implementation of
// repository.WebhookDeliveryRepository.
type WebhookRepository struct {
	pool *pgxpool.Pool
}

// NewWebhookRepository wires a repository onto an existing pool.
func NewWebhookRepository(pool *pgxpool.Pool) *WebhookRepository {
	return &WebhookRepository{pool: pool}
}

var _ repository.WebhookDeliveryRepository = (*WebhookRepository)(nil)

// Create implements repository.WebhookDeliveryRepository.
func (r *WebhookRepository) Create(ctx context.Context, d *domain.WebhookDelivery) error {
	const q = `
		INSERT INTO webhook_deliveries (
			id, transaction_id, merchant_id, environment, event_type, url, payload, status,
			attempts, max_attempts, last_error, last_attempt_at, next_attempt_at,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`
	_, err := r.pool.Exec(ctx, q,
		d.ID, d.TransactionID, d.MerchantID, d.Environment, d.EventType, d.URL, d.Payload, d.Status,
		d.Attempts, d.MaxAttempts, d.LastError, d.LastAttemptAt, d.NextAttemptAt,
		d.CreatedAt, d.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: inserting webhook delivery: %w", err)
	}
	return nil
}

// Update implements repository.WebhookDeliveryRepository.
func (r *WebhookRepository) Update(ctx context.Context, d *domain.WebhookDelivery) error {
	const q = `
		UPDATE webhook_deliveries SET
			status          = $2,
			attempts        = $3,
			last_error      = $4,
			last_attempt_at = $5,
			next_attempt_at = $6,
			updated_at      = $7
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q,
		d.ID, d.Status, d.Attempts, d.LastError, d.LastAttemptAt, d.NextAttemptAt, d.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: updating webhook delivery: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: webhook delivery %s: %w", d.ID, domain.ErrNotFound)
	}
	return nil
}

// GetByID implements repository.WebhookDeliveryRepository.
func (r *WebhookRepository) GetByID(ctx context.Context, id string) (*domain.WebhookDelivery, error) {
	const q = `SELECT ` + deliveryColumns + ` FROM webhook_deliveries WHERE id = $1`
	d, err := scanDelivery(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: querying webhook delivery: %w", err)
	}
	return d, nil
}

// Due implements repository.WebhookDeliveryRepository. It returns pending
// deliveries whose scheduled attempt time has passed, oldest first.
func (r *WebhookRepository) Due(ctx context.Context, now time.Time, limit int) ([]*domain.WebhookDelivery, error) {
	const q = `SELECT ` + deliveryColumns + `
		FROM webhook_deliveries
		WHERE status = 'pending'
		  AND (next_attempt_at IS NULL OR next_attempt_at <= $1)
		ORDER BY created_at ASC
		LIMIT $2`
	rows, err := r.pool.Query(ctx, q, now, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing due webhook deliveries: %w", err)
	}
	defer rows.Close()

	var out []*domain.WebhookDelivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning webhook deliveries: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating webhook deliveries: %w", err)
	}
	return out, nil
}

// List implements repository.WebhookDeliveryRepository. It powers the admin
// console's outbound deliveries tab.
func (r *WebhookRepository) List(ctx context.Context, f repository.DeliveryFilter) ([]*domain.WebhookDelivery, error) {
	var (
		where []string
		args  []any
	)
	if f.Environment != "" {
		args = append(args, f.Environment)
		where = append(where, fmt.Sprintf("environment = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, string(f.Status))
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}

	q := `SELECT ` + deliveryColumns + ` FROM webhook_deliveries`
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
		return nil, fmt.Errorf("postgres: listing webhook deliveries: %w", err)
	}
	defer rows.Close()

	var out []*domain.WebhookDelivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning webhook delivery: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating webhook deliveries: %w", err)
	}
	return out, nil
}
