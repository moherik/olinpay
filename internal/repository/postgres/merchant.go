package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
)

// MerchantRepository is the PostgreSQL implementation of
// repository.MerchantRepository.
type MerchantRepository struct {
	pool *pgxpool.Pool
}

// NewMerchantRepository wires a repository onto an existing pool.
func NewMerchantRepository(pool *pgxpool.Pool) *MerchantRepository {
	return &MerchantRepository{pool: pool}
}

var _ repository.MerchantRepository = (*MerchantRepository)(nil)

// Create implements repository.MerchantRepository.
func (r *MerchantRepository) Create(ctx context.Context, m *domain.Merchant) error {
	routing, err := jsonBytes(m.Routing)
	if err != nil {
		return fmt.Errorf("postgres: encoding routing: %w", err)
	}
	const q = `
		INSERT INTO merchants (
			id, name, api_key_hash, api_key_prefix, callback_url, webhook_secret,
			routing, status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
	_, err = r.pool.Exec(ctx, q,
		m.ID, m.Name, m.APIKeyHash, m.APIKeyPrefix, m.CallbackURL, m.WebhookSecret,
		routing, m.Status, m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("postgres: merchant api key: %w", domain.ErrConflict)
		}
		return fmt.Errorf("postgres: inserting merchant: %w", err)
	}
	return nil
}

// Update implements repository.MerchantRepository.
func (r *MerchantRepository) Update(ctx context.Context, m *domain.Merchant) error {
	routing, err := jsonBytes(m.Routing)
	if err != nil {
		return fmt.Errorf("postgres: encoding routing: %w", err)
	}
	const q = `
		UPDATE merchants SET
			name           = $2,
			callback_url   = $3,
			webhook_secret = $4,
			routing        = $5,
			status         = $6,
			updated_at     = $7
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q,
		m.ID, m.Name, m.CallbackURL, m.WebhookSecret, routing, m.Status, m.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: updating merchant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: merchant %s: %w", m.ID, domain.ErrNotFound)
	}
	return nil
}

// GetByID implements repository.MerchantRepository.
func (r *MerchantRepository) GetByID(ctx context.Context, id string) (*domain.Merchant, error) {
	const q = `SELECT ` + merchantColumns + ` FROM merchants WHERE id = $1`
	return r.queryOne(ctx, q, id)
}

// GetByAPIKeyHash implements repository.MerchantRepository.
func (r *MerchantRepository) GetByAPIKeyHash(ctx context.Context, hash string) (*domain.Merchant, error) {
	const q = `SELECT ` + merchantColumns + ` FROM merchants WHERE api_key_hash = $1`
	return r.queryOne(ctx, q, hash)
}

func (r *MerchantRepository) queryOne(ctx context.Context, q string, args ...any) (*domain.Merchant, error) {
	m, err := scanMerchant(r.pool.QueryRow(ctx, q, args...))
	if err != nil {
		if isNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: querying merchant: %w", err)
	}
	return m, nil
}

// List implements repository.MerchantRepository.
func (r *MerchantRepository) List(ctx context.Context, limit, offset int) ([]*domain.Merchant, error) {
	const q = `SELECT ` + merchantColumns + `
		FROM merchants ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := r.pool.Query(ctx, q, clampLimit(limit), max(offset, 0))
	if err != nil {
		return nil, fmt.Errorf("postgres: listing merchants: %w", err)
	}
	defer rows.Close()

	var out []*domain.Merchant
	for rows.Next() {
		m, err := scanMerchant(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning merchant: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating merchants: %w", err)
	}
	return out, nil
}
