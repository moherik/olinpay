package domain

import (
	"strings"
	"time"
)

// Environments are the isolation levels a payment platform is operated in.
// The environment of a transaction or delivery comes from the environment of
// the API key that produced it: sandbox keys can never produce production
// data and the other way around.
const (
	EnvironmentSandbox    = "sandbox"
	EnvironmentProduction = "production"
)

// EnvironmentValid reports whether raw is one of the supported environments.
func EnvironmentValid(raw string) bool {
	switch raw {
	case EnvironmentSandbox, EnvironmentProduction:
		return true
	default:
		return false
	}
}

// APIKey statuses.
const (
	APIKeyActive  = "active"
	APIKeyRevoked = "revoked"
)

// APIKey is a merchant credential issued by the admin console. Only its hash
// and a short non-secret prefix are persisted; the raw key is shown exactly
// once at creation.
type APIKey struct {
	ID          string     `json:"id"`
	MerchantID  string     `json:"merchant_id"`
	Label       string     `json:"label"`
	KeyHash     string     `json:"-"`
	KeyPrefix   string     `json:"key_prefix"`
	Environment string     `json:"environment"`
	Status      string     `json:"status"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Revoked reports whether the key must no longer authenticate.
func (k *APIKey) Revoked() bool { return k.Status != APIKeyActive }

// APIKeyPrefixFor returns the display prefix of a raw key, cut to a short,
// safe-for-logs form. It mirrors id.APIKeyPrefix's conventions for the
// sk_test_/sk_live_ shaped keys the console issues.
func APIKeyPrefixFor(raw string) string {
	if i := strings.IndexByte(raw, '_'); i > 0 {
		if end := i + 1 + 8; end < len(raw) {
			return raw[:end]
		}
	}
	if len(raw) > 12 {
		return raw[:12]
	}
	return raw
}

// AdminRole is a console account role, ordered most to least privileged.
type AdminRole string

const (
	// RoleOwner can do everything, including users, provider credentials and
	// key issuance.
	RoleOwner AdminRole = "owner"
	// RoleOperator can run operations: merchants, transaction cancels, webhook
	// retries. Cannot manage users or provider credentials.
	RoleOperator AdminRole = "operator"
	// RoleTech has read access plus webhook retries and routing changes.
	// Cannot move money or change merchants.
	RoleTech AdminRole = "tech"
)

// Valid reports whether the role is one of the supported values.
func (r AdminRole) Valid() bool {
	switch r {
	case RoleOwner, RoleOperator, RoleTech:
		return true
	default:
		return false
	}
}

// rank is used for comparisons; higher wins.
func (r AdminRole) rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleOperator:
		return 2
	case RoleTech:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether the role carries at least the privileges of other.
func (r AdminRole) AtLeast(other AdminRole) bool { return r.rank() >= other.rank() }

// Console account statuses.
const (
	AdminUserActive    = "active"
	AdminUserSuspended = "suspended"
)

// AdminUser is a console account. The password hash never leaves the database.
type AdminUser struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	PasswordHash string     `json:"-"`
	Role         AdminRole  `json:"role"`
	Status       string     `json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// AdminSession authenticates a console browser session. Only the SHA-256 hash
// of the session token is stored, so the raw token lives only in the cookie.
type AdminSession struct {
	TokenHash string    `json:"-"`
	UserID    string    `json:"user_id"`
	IP        string    `json:"ip,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Expired reports whether the session must be rejected.
func (s *AdminSession) Expired(now time.Time) bool { return now.After(s.ExpiresAt) }

// AuditEntry is one append-only console audit record.
type AuditEntry struct {
	ID     string    `json:"id"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target,omitempty"`
	IP     string    `json:"ip,omitempty"`
	At     time.Time `json:"at"`
}

// InboundEvent is the persisted record of one gateway callback received by the
// webhook receiver: whether its signature checked out, whether it could be
// parsed, and which transaction it was applied to. It is what the console's
// "Inbound callbacks" tab lists and what reprocessing replays.
type InboundEvent struct {
	ID                string    `json:"id"`
	Provider          string    `json:"provider"`
	Environment       string    `json:"environment"`
	TransactionID     string    `json:"transaction_id,omitempty"`
	EventType         string    `json:"event_type,omitempty"`
	ReferenceID       string    `json:"reference_id,omitempty"`
	ProviderReference string    `json:"provider_reference,omitempty"`
	Amount            int64     `json:"amount"`
	Status            string    `json:"status,omitempty"`
	SignatureValid    bool      `json:"signature_valid"`
	Parsed            bool      `json:"parsed"`
	Applied           bool      `json:"applied"`
	Note              string    `json:"note,omitempty"`
	RemoteAddr        string    `json:"remote_addr,omitempty"`
	ReceivedAt        time.Time `json:"received_at"`
}

// ProviderConfig is a DB-stored credential set for one provider adapter in one
// environment. Values are write-only via the admin API.
type ProviderConfig struct {
	Provider    string            `json:"provider"`
	Environment string            `json:"environment"`
	Config      map[string]string `json:"-"`
	UpdatedAt   time.Time         `json:"updated_at"`
	UpdatedBy   string            `json:"updated_by"`
}

// InstanceName maps a provider adapter and environment onto the provider
// instance name registered in the runtime registry. Environment-built names
// are always suffixed, so sandbox and production instances of one adapter are
// distinct and cannot be mixed across environments. The unsuffixed name is
// reserved for legacy environment-variable configured providers, which serve
// both environments.
func InstanceName(provider, environment string) string {
	return provider + "." + environment
}
