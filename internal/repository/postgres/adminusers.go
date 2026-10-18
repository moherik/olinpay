package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
)

const adminUserColumns = `id, email, name, password_hash, role, status, last_login_at,
	created_at, updated_at`

func scanAdminUser(s scannable) (*domain.AdminUser, error) {
	var u domain.AdminUser
	err := s.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.Status,
		&u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// AdminUserRepository is the PostgreSQL implementation of
// repository.AdminUserRepository.
type AdminUserRepository struct {
	pool *pgxpool.Pool
}

// NewAdminUserRepository wires a repository onto an existing pool.
func NewAdminUserRepository(pool *pgxpool.Pool) *AdminUserRepository {
	return &AdminUserRepository{pool: pool}
}

var _ repository.AdminUserRepository = (*AdminUserRepository)(nil)

// Create implements repository.AdminUserRepository.
func (r *AdminUserRepository) Create(ctx context.Context, u *domain.AdminUser) error {
	const q = `
		INSERT INTO admin_users (id, email, name, password_hash, role, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	_, err := r.pool.Exec(ctx, q,
		u.ID, u.Email, u.Name, u.PasswordHash, u.Role, u.Status, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("postgres: admin user email: %w", domain.ErrConflict)
		}
		return fmt.Errorf("postgres: inserting admin user: %w", err)
	}
	return nil
}

// Update implements repository.AdminUserRepository.
func (r *AdminUserRepository) Update(ctx context.Context, u *domain.AdminUser) error {
	const q = `
		UPDATE admin_users SET
			name = $2, role = $3, status = $4, last_login_at = $5, updated_at = $6
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q,
		u.ID, u.Name, u.Role, u.Status, u.LastLoginAt, u.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: updating admin user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: admin user %s: %w", u.ID, domain.ErrNotFound)
	}
	return nil
}

// GetByID implements repository.AdminUserRepository.
func (r *AdminUserRepository) GetByID(ctx context.Context, id string) (*domain.AdminUser, error) {
	const q = `SELECT ` + adminUserColumns + ` FROM admin_users WHERE id = $1`
	return r.queryAdminUser(ctx, q, id)
}

// GetByEmail implements repository.AdminUserRepository.
func (r *AdminUserRepository) GetByEmail(ctx context.Context, email string) (*domain.AdminUser, error) {
	const q = `SELECT ` + adminUserColumns + ` FROM admin_users WHERE email = $1`
	return r.queryAdminUser(ctx, q, email)
}

// List implements repository.AdminUserRepository.
func (r *AdminUserRepository) List(ctx context.Context) ([]*domain.AdminUser, error) {
	const q = `SELECT ` + adminUserColumns + ` FROM admin_users ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing admin users: %w", err)
	}
	defer rows.Close()

	var out []*domain.AdminUser
	for rows.Next() {
		u, err := scanAdminUser(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning admin user: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating admin users: %w", err)
	}
	return out, nil
}

// Count implements repository.AdminUserRepository.
func (r *AdminUserRepository) Count(ctx context.Context) (int64, error) {
	const q = `SELECT count(*) FROM admin_users`
	var n int64
	if err := r.pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: counting admin users: %w", err)
	}
	return n, nil
}

func (r *AdminUserRepository) queryAdminUser(ctx context.Context, q string, args ...any) (*domain.AdminUser, error) {
	u, err := scanAdminUser(r.pool.QueryRow(ctx, q, args...))
	if err != nil {
		if isNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: querying admin user: %w", err)
	}
	return u, nil
}

// AdminSessionRepository is the PostgreSQL implementation of
// repository.AdminSessionRepository.
type AdminSessionRepository struct {
	pool *pgxpool.Pool
}

// NewAdminSessionRepository wires a repository onto an existing pool.
func NewAdminSessionRepository(pool *pgxpool.Pool) *AdminSessionRepository {
	return &AdminSessionRepository{pool: pool}
}

var _ repository.AdminSessionRepository = (*AdminSessionRepository)(nil)

// Create implements repository.AdminSessionRepository.
func (r *AdminSessionRepository) Create(ctx context.Context, s *domain.AdminSession) error {
	const q = `
		INSERT INTO admin_sessions (token_hash, user_id, ip, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5)`
	_, err := r.pool.Exec(ctx, q, s.TokenHash, s.UserID, s.IP, s.ExpiresAt, s.CreatedAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting admin session: %w", err)
	}
	return nil
}

// Get implements repository.AdminSessionRepository.
func (r *AdminSessionRepository) Get(ctx context.Context, tokenHash string, now time.Time) (*domain.AdminSession, error) {
	const q = `
		SELECT token_hash, user_id, ip, expires_at, created_at
		FROM admin_sessions WHERE token_hash = $1 AND expires_at > $2`
	s, err := scanSession(r.pool.QueryRow(ctx, q, tokenHash, now))
	if err != nil {
		if isNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: querying admin session: %w", err)
	}
	return s, nil
}

// Delete implements repository.AdminSessionRepository.
func (r *AdminSessionRepository) Delete(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE token_hash = $1`, tokenHash)
	if err != nil {
		return fmt.Errorf("postgres: deleting admin session: %w", err)
	}
	return nil
}

// DeleteForUser implements repository.AdminSessionRepository.
func (r *AdminSessionRepository) DeleteForUser(ctx context.Context, userID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("postgres: deleting admin sessions for user: %w", err)
	}
	return nil
}

func scanSession(s scannable) (*domain.AdminSession, error) {
	var sess domain.AdminSession
	err := s.Scan(&sess.TokenHash, &sess.UserID, &sess.IP, &sess.ExpiresAt, &sess.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// AuditRepository is the PostgreSQL implementation of repository.AuditRepository.
type AuditRepository struct {
	pool *pgxpool.Pool
}

// NewAuditRepository wires a repository onto an existing pool.
func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository {
	return &AuditRepository{pool: pool}
}

var _ repository.AuditRepository = (*AuditRepository)(nil)

// Create implements repository.AuditRepository.
func (r *AuditRepository) Create(ctx context.Context, e *domain.AuditEntry) error {
	const q = `INSERT INTO audit_log (id, actor, action, target, ip, at) VALUES ($1,$2,$3,$4,$5,$6)`
	_, err := r.pool.Exec(ctx, q, e.ID, e.Actor, e.Action, e.Target, e.IP, e.At)
	if err != nil {
		return fmt.Errorf("postgres: inserting audit entry: %w", err)
	}
	return nil
}

// List implements repository.AuditRepository.
func (r *AuditRepository) List(ctx context.Context, f repository.AuditFilter) ([]*domain.AuditEntry, error) {
	var (
		where []string
		args  []any
	)
	if s := strings.TrimSpace(f.Search); s != "" {
		needle := "%" + s + "%"
		args = append(args, needle, needle, needle)
		where = append(where, fmt.Sprintf(
			"(actor ILIKE $%d OR action ILIKE $%d OR target ILIKE $%d)",
			len(args)-2, len(args)-1, len(args)))
	}

	q := `SELECT id, actor, action, target, ip, at FROM audit_log`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY at DESC"
	q += fmt.Sprintf(" LIMIT $%d", len(args)+1)
	args = append(args, clampLimit(f.Limit))
	q += fmt.Sprintf(" OFFSET $%d", len(args)+1)
	args = append(args, max(f.Offset, 0))

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing audit entries: %w", err)
	}
	defer rows.Close()

	var out []*domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &e.IP, &e.At); err != nil {
			return nil, fmt.Errorf("postgres: scanning audit entry: %w", err)
		}
		out = append(out, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating audit entries: %w", err)
	}
	return out, nil
}

const apiKeyColumns = `id, merchant_id, label, key_hash, key_prefix, environment, status,
	last_used_at, created_at, updated_at`

func scanAPIKey(s scannable) (*domain.APIKey, error) {
	var k domain.APIKey
	err := s.Scan(&k.ID, &k.MerchantID, &k.Label, &k.KeyHash, &k.KeyPrefix, &k.Environment,
		&k.Status, &k.LastUsedAt, &k.CreatedAt, &k.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// APIKeyRepository is the PostgreSQL implementation of repository.APIKeyRepository.
type APIKeyRepository struct {
	pool *pgxpool.Pool
}

// NewAPIKeyRepository wires a repository onto an existing pool.
func NewAPIKeyRepository(pool *pgxpool.Pool) *APIKeyRepository {
	return &APIKeyRepository{pool: pool}
}

var _ repository.APIKeyRepository = (*APIKeyRepository)(nil)

// Create implements repository.APIKeyRepository.
func (r *APIKeyRepository) Create(ctx context.Context, k *domain.APIKey) error {
	const q = `
		INSERT INTO api_keys (id, merchant_id, label, key_hash, key_prefix, environment,
			status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.pool.Exec(ctx, q,
		k.ID, k.MerchantID, k.Label, k.KeyHash, k.KeyPrefix, k.Environment,
		k.Status, k.CreatedAt, k.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("postgres: api key hash: %w", domain.ErrConflict)
		}
		return fmt.Errorf("postgres: inserting api key: %w", err)
	}
	return nil
}

// Update implements repository.APIKeyRepository.
func (r *APIKeyRepository) Update(ctx context.Context, k *domain.APIKey) error {
	const q = `
		UPDATE api_keys SET label = $2, status = $3, last_used_at = $4, updated_at = $5
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, k.ID, k.Label, k.Status, k.LastUsedAt, k.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: updating api key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: api key %s: %w", k.ID, domain.ErrNotFound)
	}
	return nil
}

// GetByID implements repository.APIKeyRepository.
func (r *APIKeyRepository) GetByID(ctx context.Context, id string) (*domain.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE id = $1`
	return r.queryKey(ctx, q, id)
}

// GetByHash implements repository.APIKeyRepository.
func (r *APIKeyRepository) GetByHash(ctx context.Context, hash string) (*domain.APIKey, error) {
	const q = `SELECT ` + apiKeyColumns + ` FROM api_keys WHERE key_hash = $1`
	return r.queryKey(ctx, q, hash)
}

// List implements repository.APIKeyRepository.
func (r *APIKeyRepository) List(ctx context.Context, f repository.APIKeyFilter) ([]*domain.APIKey, error) {
	var (
		where []string
		args  []any
	)
	if f.MerchantID != "" {
		args = append(args, f.MerchantID)
		where = append(where, fmt.Sprintf("merchant_id = $%d", len(args)))
	}
	if f.Environment != "" {
		args = append(args, f.Environment)
		where = append(where, fmt.Sprintf("environment = $%d", len(args)))
	}

	q := `SELECT ` + apiKeyColumns + ` FROM api_keys`
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
		return nil, fmt.Errorf("postgres: listing api keys: %w", err)
	}
	defer rows.Close()

	var out []*domain.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning api key: %w", err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating api keys: %w", err)
	}
	return out, nil
}

// EnvironmentsOfMerchant implements repository.APIKeyRepository.
func (r *APIKeyRepository) EnvironmentsOfMerchant(ctx context.Context, merchantID string) ([]string, error) {
	const q = `
		SELECT DISTINCT environment FROM api_keys
		WHERE merchant_id = $1 AND status = 'active'
		ORDER BY environment`
	rows, err := r.pool.Query(ctx, q, merchantID)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing merchant key environments: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var env string
		if err := rows.Scan(&env); err != nil {
			return nil, fmt.Errorf("postgres: scanning key environment: %w", err)
		}
		out = append(out, env)
	}
	return out, rows.Err()
}

func (r *APIKeyRepository) queryKey(ctx context.Context, q string, args ...any) (*domain.APIKey, error) {
	k, err := scanAPIKey(r.pool.QueryRow(ctx, q, args...))
	if err != nil {
		if isNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: querying api key: %w", err)
	}
	return k, nil
}

// InboundEventRepository is the PostgreSQL implementation of
// repository.InboundEventRepository.
type InboundEventRepository struct {
	pool *pgxpool.Pool
}

// NewInboundEventRepository wires a repository onto an existing pool.
func NewInboundEventRepository(pool *pgxpool.Pool) *InboundEventRepository {
	return &InboundEventRepository{pool: pool}
}

var _ repository.InboundEventRepository = (*InboundEventRepository)(nil)

// Create implements repository.InboundEventRepository.
func (r *InboundEventRepository) Create(ctx context.Context, e *domain.InboundEvent) error {
	const q = `
		INSERT INTO inbound_events (
			id, provider, environment, transaction_id, event_type, reference_id,
			provider_reference, amount, status, signature_valid, parsed, applied,
			note, remote_addr, received_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`
	_, err := r.pool.Exec(ctx, q,
		e.ID, e.Provider, e.Environment, e.TransactionID, e.EventType, e.ReferenceID,
		e.ProviderReference, e.Amount, e.Status, e.SignatureValid, e.Parsed, e.Applied,
		e.Note, e.RemoteAddr, e.ReceivedAt)
	if err != nil {
		return fmt.Errorf("postgres: inserting inbound event: %w", err)
	}
	return nil
}

// GetByID implements repository.InboundEventRepository.
func (r *InboundEventRepository) GetByID(ctx context.Context, id string) (*domain.InboundEvent, error) {
	const q = `SELECT ` + inboundColumns + ` FROM inbound_events WHERE id = $1`
	e, err := scanInbound(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: querying inbound event: %w", err)
	}
	return e, nil
}

// Update implements repository.InboundEventRepository. Only the reprocessing
// outcome fields are written; the history stays otherwise immutable.
func (r *InboundEventRepository) Update(ctx context.Context, e *domain.InboundEvent) error {
	const q = `
		UPDATE inbound_events SET
			environment = $2, transaction_id = $3, applied = $4, note = $5
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q,
		e.ID, e.Environment, e.TransactionID, e.Applied, e.Note)
	if err != nil {
		return fmt.Errorf("postgres: updating inbound event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: inbound event %s: %w", e.ID, domain.ErrNotFound)
	}
	return nil
}

// List implements repository.InboundEventRepository.
func (r *InboundEventRepository) List(ctx context.Context, f repository.InboundFilter) ([]*domain.InboundEvent, error) {
	var (
		where []string
		args  []any
	)
	// Unknown-environment callbacks (uncorrelated at receive time) stay visible
	// under both environment filters: an operator must not lose callbacks just
	// because no transaction matched yet.
	if f.Environment != "" {
		args = append(args, f.Environment)
		where = append(where, fmt.Sprintf(
			"(environment = $%d OR environment = '')", len(args)))
	}
	if f.TransactionID != "" {
		args = append(args, f.TransactionID)
		where = append(where, fmt.Sprintf("transaction_id = $%d", len(args)))
	}

	q := `SELECT ` + inboundColumns + ` FROM inbound_events`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY received_at DESC"
	q += fmt.Sprintf(" LIMIT $%d", len(args)+1)
	args = append(args, clampLimit(f.Limit))
	q += fmt.Sprintf(" OFFSET $%d", len(args)+1)
	args = append(args, max(f.Offset, 0))

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing inbound events: %w", err)
	}
	defer rows.Close()

	var out []*domain.InboundEvent
	for rows.Next() {
		e, err := scanInbound(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scanning inbound event: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating inbound events: %w", err)
	}
	return out, nil
}

const inboundColumns = `id, provider, environment, transaction_id, event_type, reference_id,
	provider_reference, amount, status, signature_valid, parsed, applied, note, remote_addr,
	received_at`

func scanInbound(s scannable) (*domain.InboundEvent, error) {
	var e domain.InboundEvent
	err := s.Scan(
		&e.ID, &e.Provider, &e.Environment, &e.TransactionID, &e.EventType, &e.ReferenceID,
		&e.ProviderReference, &e.Amount, &e.Status, &e.SignatureValid, &e.Parsed, &e.Applied,
		&e.Note, &e.RemoteAddr, &e.ReceivedAt,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ProviderConfigRepository is the PostgreSQL implementation of
// repository.ProviderConfigRepository.
type ProviderConfigRepository struct {
	pool *pgxpool.Pool
}

// NewProviderConfigRepository wires a repository onto an existing pool.
func NewProviderConfigRepository(pool *pgxpool.Pool) *ProviderConfigRepository {
	return &ProviderConfigRepository{pool: pool}
}

var _ repository.ProviderConfigRepository = (*ProviderConfigRepository)(nil)

// Upsert implements repository.ProviderConfigRepository.
func (r *ProviderConfigRepository) Upsert(ctx context.Context, pc *domain.ProviderConfig) error {
	config, err := jsonBytes(pc.Config)
	if err != nil {
		return fmt.Errorf("postgres: encoding provider config: %w", err)
	}
	const q = `
		INSERT INTO provider_configs (provider, environment, config, updated_at, updated_by)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (provider, environment) DO UPDATE SET
			config = EXCLUDED.config,
			updated_at = EXCLUDED.updated_at,
			updated_by = EXCLUDED.updated_by`
	_, err = r.pool.Exec(ctx, q, pc.Provider, pc.Environment, config, pc.UpdatedAt, pc.UpdatedBy)
	if err != nil {
		return fmt.Errorf("postgres: upserting provider config: %w", err)
	}
	return nil
}

// Get implements repository.ProviderConfigRepository.
func (r *ProviderConfigRepository) Get(ctx context.Context, provider, environment string) (*domain.ProviderConfig, error) {
	const q = `SELECT provider, environment, config, updated_at, updated_by
		FROM provider_configs WHERE provider = $1 AND environment = $2`
	pc, err := r.queryOne(ctx, q, provider, environment)
	if err != nil {
		return nil, err
	}
	return pc, nil
}

// List implements repository.ProviderConfigRepository.
func (r *ProviderConfigRepository) List(ctx context.Context) ([]*domain.ProviderConfig, error) {
	const q = `SELECT provider, environment, config, updated_at, updated_by
		FROM provider_configs ORDER BY provider, environment`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing provider configs: %w", err)
	}
	defer rows.Close()

	var out []*domain.ProviderConfig
	for rows.Next() {
		var pc domain.ProviderConfig
		var raw []byte
		if err := rows.Scan(&pc.Provider, &pc.Environment, &raw, &pc.UpdatedAt, &pc.UpdatedBy); err != nil {
			return nil, fmt.Errorf("postgres: scanning provider config: %w", err)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &pc.Config); err != nil {
				return nil, fmt.Errorf("postgres: decoding provider config: %w", err)
			}
		}
		out = append(out, &pc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating provider configs: %w", err)
	}
	return out, nil
}

// Delete implements repository.ProviderConfigRepository.
func (r *ProviderConfigRepository) Delete(ctx context.Context, provider, environment string) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM provider_configs WHERE provider = $1 AND environment = $2`,
		provider, environment)
	if err != nil {
		return fmt.Errorf("postgres: deleting provider config: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: provider config %s/%s: %w", provider, environment, domain.ErrNotFound)
	}
	return nil
}

func (r *ProviderConfigRepository) queryOne(ctx context.Context, q string, args ...any) (*domain.ProviderConfig, error) {
	row := r.pool.QueryRow(ctx, q, args...)
	var pc domain.ProviderConfig
	var raw []byte
	if err := row.Scan(&pc.Provider, &pc.Environment, &raw, &pc.UpdatedAt, &pc.UpdatedBy); err != nil {
		if isNoRows(err) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("postgres: querying provider config: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &pc.Config); err != nil {
			return nil, fmt.Errorf("postgres: decoding provider config: %w", err)
		}
	}
	return &pc, nil
}
