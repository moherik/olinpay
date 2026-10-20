// Admin-facing persistence contracts: console accounts, sessions, the audit
// log, merchant API keys, inbound callback history and provider credentials.
// They live beside the core repositories so implementations keep the same
// PostgreSQL / in-memory split; the core engine itself never uses them.

package repository

import (
	"context"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
)

// AdminUserRepository persists console accounts.
type AdminUserRepository interface {
	// Create inserts a console user. It returns domain.ErrConflict when the
	// email is already taken.
	Create(ctx context.Context, u *domain.AdminUser) error
	// CreateFirst inserts the first console user. It returns domain.ErrConflict
	// when any console user already exists.
	CreateFirst(ctx context.Context, u *domain.AdminUser) error
	// Update persists mutable fields (name, role, status, last login).
	Update(ctx context.Context, u *domain.AdminUser) error
	// GetByID loads a user by identifier.
	GetByID(ctx context.Context, id string) (*domain.AdminUser, error)
	// GetByEmail loads a user by its unique email (login lookup).
	GetByEmail(ctx context.Context, email string) (*domain.AdminUser, error)
	// List returns console users, newest first.
	List(ctx context.Context) ([]*domain.AdminUser, error)
	// Count reports how many console accounts exist. A zero count means the
	// console still needs its bootstrap user.
	Count(ctx context.Context) (int64, error)
}

// AdminSessionRepository persists console browser sessions. Expiry is stored,
// not assumed, so a restarted process enforces the same TTL.
type AdminSessionRepository interface {
	// Create stores a session keyed by token hash.
	Create(ctx context.Context, s *domain.AdminSession) error
	// Get returns the unexpired session for a token hash, or
	// domain.ErrNotFound when the token is unknown or stale.
	Get(ctx context.Context, tokenHash string, now time.Time) (*domain.AdminSession, error)
	// Delete removes one session (logout).
	Delete(ctx context.Context, tokenHash string) error
	// DeleteForUser invalidates every session of a user (suspend/role change).
	DeleteForUser(ctx context.Context, userID string) error
}

// AuditFilter narrows an audit listing query.
type AuditFilter struct {
	// Search matches loosely against actor, action and target.
	Search string
	Limit  int
	Offset int
}

// AuditRepository persists the append-only console audit log.
type AuditRepository interface {
	// Create appends one entry. Nothing in the console updates or deletes
	// entries; correctness of the history depends on that.
	Create(ctx context.Context, e *domain.AuditEntry) error
	// List returns entries, newest first.
	List(ctx context.Context, f AuditFilter) ([]*domain.AuditEntry, error)
}

// APIKeyRepository persists merchant API keys.
type APIKeyRepository interface {
	// Create inserts a key. It returns domain.ErrConflict when the key hash is
	// already in use.
	Create(ctx context.Context, k *domain.APIKey) error
	// Update persists mutable fields (label, status, last_used_at).
	Update(ctx context.Context, k *domain.APIKey) error
	// GetByID loads a key by identifier.
	GetByID(ctx context.Context, id string) (*domain.APIKey, error)
	// GetByHash resolves a key from its SHA-256 hash (auth lookup).
	GetByHash(ctx context.Context, hash string) (*domain.APIKey, error)
	// List returns keys matching the filter, newest first.
	List(ctx context.Context, f APIKeyFilter) ([]*domain.APIKey, error)
	// EnvironmentsOfMerchant returns the distinct key environments a merchant
	// holds. It drives the environment tags on the merchants list.
	EnvironmentsOfMerchant(ctx context.Context, merchantID string) ([]string, error)
}

// APIKeyFilter narrows an API key listing query.
type APIKeyFilter struct {
	MerchantID  string
	Environment string
	Limit       int
	Offset      int
}

// InboundFilter narrows an inbound callback listing query.
type InboundFilter struct {
	Environment   string
	TransactionID string
	Limit         int
	Offset        int
}

// InboundEventRepository persists the gateway callback history.
type InboundEventRepository interface {
	// Create appends one inbound callback record.
	Create(ctx context.Context, e *domain.InboundEvent) error
	// GetByID loads one record.
	GetByID(ctx context.Context, id string) (*domain.InboundEvent, error)
	// List returns records, newest first.
	List(ctx context.Context, f InboundFilter) ([]*domain.InboundEvent, error)
	// Update persists the reprocessing result (applied flag, correlation) of a
	// stored callback. Nothing else in the history is mutable.
	Update(ctx context.Context, e *domain.InboundEvent) error
}

// ProviderConfigRepository persists DB-stored provider credentials, one row
// per (adapter, environment).
type ProviderConfigRepository interface {
	// Upsert writes the credential set for (provider, environment).
	Upsert(ctx context.Context, pc *domain.ProviderConfig) error
	// Get loads one row or returns domain.ErrNotFound.
	Get(ctx context.Context, provider, environment string) (*domain.ProviderConfig, error)
	// List returns every stored credential set.
	List(ctx context.Context) ([]*domain.ProviderConfig, error)
	// Delete removes one row (credential removal in the console).
	Delete(ctx context.Context, provider, environment string) error
}
