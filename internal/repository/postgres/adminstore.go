package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/pkg/provider"
)

// AdminStore is the PostgreSQL implementation of repository.AdminStore, the
// cross-entity aggregates the admin console pages are built on.
type AdminStore struct {
	pool *pgxpool.Pool
}

// NewAdminStore wires the store onto an existing pool.
func NewAdminStore(pool *pgxpool.Pool) *AdminStore {
	return &AdminStore{pool: pool}
}

var _ repository.AdminStore = (*AdminStore)(nil)

// envScope returns the SQL clause and args that scope a query to one
// environment. An empty environment (the console's "all" mode) returns no
// clause and no args.
func envScope(environment, column string) (string, []any) {
	if environment == "" {
		return "", nil
	}
	return fmt.Sprintf(" AND %s = $1", column), []any{environment}
}

// MerchantsWithStats implements repository.AdminStore. It lists merchants
// newest first with their active-key and 30-day transaction counts, optionally
// filtered by a name substring.
func (s *AdminStore) MerchantsWithStats(ctx context.Context, search string) ([]*repository.MerchantListing, error) {
	q := `
		SELECT ` + merchantColumnsAll + `,
			COALESCE(k.active_keys, 0)      AS active_keys,
			COALESCE(t.transactions_30d, 0) AS transactions_30d
		FROM merchants m
		LEFT JOIN (
			SELECT merchant_id, count(*) AS active_keys
			FROM api_keys WHERE status = 'active'
			GROUP BY merchant_id
		) k ON k.merchant_id = m.id
		LEFT JOIN (
			SELECT merchant_id, count(*) AS transactions_30d
			FROM transactions
			WHERE created_at > now() - interval '30 days'
			GROUP BY merchant_id
		) t ON t.merchant_id = m.id`
	var args []any
	if needle := strings.TrimSpace(search); needle != "" {
		args = append(args, "%"+needle+"%")
		q += fmt.Sprintf(" WHERE m.name ILIKE $%d", len(args))
	}
	q += " ORDER BY m.created_at DESC"

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing merchants with stats: %w", err)
	}
	defer rows.Close()

	var out []*repository.MerchantListing
	for rows.Next() {
		var (
			m     domain.Merchant
			rawKG []byte
			keys  int64
			tx30  int64
		)
		if err := rows.Scan(
			&m.ID, &m.Name, &m.APIKeyHash, &m.APIKeyPrefix, &m.CallbackURL, &m.WebhookSecret,
			&rawKG, &m.Status, &m.CreatedAt, &m.UpdatedAt,
			&keys, &tx30,
		); err != nil {
			return nil, fmt.Errorf("postgres: scanning merchant listing: %w", err)
		}
		if err := decodeJSON(rawKG, &m.Routing); err != nil {
			return nil, fmt.Errorf("postgres: decoding merchant routing: %w", err)
		}
		out = append(out, &repository.MerchantListing{
			Merchant:        &m,
			ActiveKeys:      int(keys),
			Transactions30d: int(tx30),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating merchant listings: %w", err)
	}
	return out, nil
}

// merchantColumnsAll is the merchant projection qualified for the joined query
// above (merchants appears under the alias m).
const merchantColumnsAll = `m.id, m.name, m.api_key_hash, m.api_key_prefix, m.callback_url,
	m.webhook_secret, m.routing, m.status, m.created_at, m.updated_at`

// Overview implements repository.AdminStore. Environment scopes the numbers;
// the empty string means all environments.
func (s *AdminStore) Overview(ctx context.Context, environment string) (*repository.OverviewStats, error) {
	out := &repository.OverviewStats{
		CreatedPerHour: make([]int64, 24),
		FailedPerHour:  make([]int64, 24),
	}

	// Status breakdown, zero-filled over the unified status set so the console
	// never implies a status exists only because it produced a row.
	clause, args := envScope(environment, "environment")
	rows, err := s.pool.Query(ctx, `
		SELECT status, count(*) FROM transactions WHERE TRUE`+clause+`
		GROUP BY status`, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting transactions by status: %w", err)
	}
	breakdown := map[string]int64{}
	for rows.Next() {
		var (
			status string
			n      int64
		)
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: scanning status breakdown: %w", err)
		}
		breakdown[status] = n
		out.TotalTransactions += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating status breakdown: %w", err)
	}
	for _, status := range []string{
		"paid", "pending", "expired", "failed", "cancelled", "refunded", "unknown",
	} {
		out.Statuses = append(out.Statuses, repository.StatusCount{
			Status: provider.Status(status),
			Count:  breakdown[status],
		})
	}

	// Hourly buckets over a generated series so empty hours stay at zero. Rows
	// are oldest-first, matching the array index order.
	clause, args = envScope(environment, "t.environment")
	hourRows, err := s.pool.Query(ctx, `
		WITH hours AS (
			SELECT generate_series(
				date_trunc('hour', now()) - interval '23 hours',
				date_trunc('hour', now()),
				interval '1 hour') AS h
		)
		SELECT
			count(t.id)                                   AS created,
			count(t.id) FILTER (WHERE t.status = 'failed') AS failed
		FROM hours h
		LEFT JOIN transactions t
			ON date_trunc('hour', t.created_at) = h.h`+clause+`
		GROUP BY h.h
		ORDER BY h.h`, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting payments per hour: %w", err)
	}
	idx := 0
	for hourRows.Next() && idx < 24 {
		if err := hourRows.Scan(&out.CreatedPerHour[idx], &out.FailedPerHour[idx]); err != nil {
			hourRows.Close()
			return nil, fmt.Errorf("postgres: scanning hourly counts: %w", err)
		}
		idx++
	}
	hourRows.Close()
	if err := hourRows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating hourly counts: %w", err)
	}

	// Delivery state counts.
	clause, args = envScope(environment, "environment")
	var delivered, pending, failed int64
	if err := s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status = 'delivered'),
			count(*) FILTER (WHERE status = 'pending'),
			count(*) FILTER (WHERE status = 'failed')
		FROM webhook_deliveries WHERE TRUE`+clause, args...).Scan(
		&delivered, &pending, &failed,
	); err != nil {
		return nil, fmt.Errorf("postgres: counting deliveries: %w", err)
	}
	out.DeliveredDeliveries = delivered
	out.PendingDeliveries = pending
	out.FailedDeliveries = failed

	// Provider reliability over the last 30 days.
	clause, args = envScope(environment, "environment")
	prel, err := s.pool.Query(ctx, `
		SELECT provider_name, count(*), count(*) FILTER (WHERE status = 'failed')
		FROM transactions
		WHERE created_at > now() - interval '30 days'`+clause+`
		GROUP BY provider_name
		ORDER BY count(*) DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting provider reliability: %w", err)
	}
	defer prel.Close()
	for prel.Next() {
		var r repository.ProviderReliability
		if err := prel.Scan(&r.Provider, &r.Payments, &r.Failed); err != nil {
			return nil, fmt.Errorf("postgres: scanning provider reliability: %w", err)
		}
		out.Providers = append(out.Providers, r)
	}
	if err := prel.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating provider reliability: %w", err)
	}

	return out, nil
}
