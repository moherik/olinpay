package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
)

type MerchantSettingsRepository struct{ pool *pgxpool.Pool }

func NewMerchantSettingsRepository(pool *pgxpool.Pool) *MerchantSettingsRepository {
	return &MerchantSettingsRepository{pool: pool}
}

var _ repository.MerchantSettingsRepository = (*MerchantSettingsRepository)(nil)

func (r *MerchantSettingsRepository) Get(ctx context.Context, merchantID string) (*domain.MerchantSettings, error) {
	settings := &domain.MerchantSettings{MerchantID: merchantID}
	err := r.pool.QueryRow(ctx, `SELECT return_url, success_return_url, failure_return_url, updated_at
		FROM merchant_settings WHERE merchant_id = $1`, merchantID).
		Scan(&settings.ReturnURL, &settings.SuccessReturnURL, &settings.FailureReturnURL, &settings.UpdatedAt)
	if err != nil {
		if isNoRows(err) {
			return settings, nil
		}
		return nil, fmt.Errorf("postgres: querying merchant settings: %w", err)
	}
	return settings, nil
}

func (r *MerchantSettingsRepository) Upsert(ctx context.Context, settings *domain.MerchantSettings) error {
	const q = `INSERT INTO merchant_settings
		(merchant_id, return_url, success_return_url, failure_return_url, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (merchant_id) DO UPDATE SET
		return_url = EXCLUDED.return_url,
		success_return_url = EXCLUDED.success_return_url,
		failure_return_url = EXCLUDED.failure_return_url,
		updated_at = EXCLUDED.updated_at`
	if _, err := r.pool.Exec(ctx, q, settings.MerchantID, settings.ReturnURL, settings.SuccessReturnURL, settings.FailureReturnURL, settings.UpdatedAt); err != nil {
		return fmt.Errorf("postgres: saving merchant settings: %w", err)
	}
	return nil
}
