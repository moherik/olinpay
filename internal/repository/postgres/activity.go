package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
)

type TransactionActivityRepository struct{ pool *pgxpool.Pool }

func NewTransactionActivityRepository(pool *pgxpool.Pool) *TransactionActivityRepository {
	return &TransactionActivityRepository{pool: pool}
}

var _ repository.TransactionActivityRepository = (*TransactionActivityRepository)(nil)

func (r *TransactionActivityRepository) Create(ctx context.Context, a *domain.TransactionActivity) error {
	headers, err := json.Marshal(a.Headers)
	if err != nil {
		return fmt.Errorf("postgres: encoding activity headers: %w", err)
	}
	const q = `
		INSERT INTO transaction_activities (
			id, transaction_id, merchant_id, environment, reference_id, direction, phase,
			provider, event_type, method, url, status_code, duration_ms, attempt, signature_valid, applied, headers, body, error, created_at
		) VALUES ($1,NULLIF($2,''),$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`
	_, err = r.pool.Exec(ctx, q,
		a.ID, a.TransactionID, a.MerchantID, a.Environment, a.ReferenceID, a.Direction, a.Phase,
		a.Provider, a.EventType, a.Method, a.URL, a.StatusCode, a.DurationMs, a.Attempt, a.SignatureValid, a.Applied, headers, a.Body, a.Error, a.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: inserting transaction activity: %w", err)
	}
	return nil
}

func (r *TransactionActivityRepository) List(ctx context.Context, f repository.ActivityFilter) ([]*domain.TransactionActivity, error) {
	var where []string
	var args []any
	if f.TransactionID != "" {
		args = append(args, f.TransactionID)
		id := len(args)
		if f.MerchantID != "" && f.ReferenceID != "" {
			args = append(args, f.MerchantID, f.Environment, f.ReferenceID)
			where = append(where, fmt.Sprintf("(transaction_id = $%d OR (transaction_id IS NULL AND merchant_id = $%d AND environment = $%d AND reference_id = $%d))", id, id+1, id+2, id+3))
		} else {
			where = append(where, fmt.Sprintf("transaction_id = $%d", id))
		}
	} else {
		if f.MerchantID != "" {
			args = append(args, f.MerchantID)
			where = append(where, fmt.Sprintf("merchant_id = $%d", len(args)))
		}
		if f.Environment != "" {
			args = append(args, f.Environment)
			where = append(where, fmt.Sprintf("environment = $%d", len(args)))
		}
		if f.ReferenceID != "" {
			args = append(args, f.ReferenceID)
			where = append(where, fmt.Sprintf("reference_id = $%d", len(args)))
		}
	}
	base := `SELECT id, COALESCE(transaction_id,''), merchant_id, environment, reference_id,
		direction, phase, provider, event_type, method, url, status_code, duration_ms,
		attempt, signature_valid, applied, headers, body, error, created_at FROM transaction_activities`
	if len(where) > 0 {
		base += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, clampActivityLimit(f.Limit))
	q := fmt.Sprintf("SELECT * FROM (%s ORDER BY created_at DESC, id DESC LIMIT $%d) recent ORDER BY created_at ASC, id ASC", base, len(args))
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing transaction activities: %w", err)
	}
	defer rows.Close()
	var out []*domain.TransactionActivity
	for rows.Next() {
		a := new(domain.TransactionActivity)
		var headers []byte
		if err := rows.Scan(&a.ID, &a.TransactionID, &a.MerchantID, &a.Environment, &a.ReferenceID,
			&a.Direction, &a.Phase, &a.Provider, &a.EventType, &a.Method, &a.URL,
			&a.StatusCode, &a.DurationMs, &a.Attempt, &a.SignatureValid, &a.Applied, &headers, &a.Body, &a.Error, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scanning transaction activity: %w", err)
		}
		if len(headers) > 0 {
			if err := json.Unmarshal(headers, &a.Headers); err != nil {
				return nil, fmt.Errorf("postgres: decoding transaction activity headers: %w", err)
			}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating transaction activities: %w", err)
	}
	return out, nil
}

func clampActivityLimit(limit int) int {
	if limit <= 0 || limit > 500 {
		return 200
	}
	return limit
}
