package domain

import (
	"encoding/json"
	"time"
)

// MerchantStatus is the lifecycle state of a merchant account.
type MerchantStatus string

const (
	// MerchantActive means the merchant may transact.
	MerchantActive MerchantStatus = "active"
	// MerchantInactive means the account exists but is disabled.
	MerchantInactive MerchantStatus = "inactive"
	// MerchantSuspended means the account was suspended (e.g. fraud, billing).
	MerchantSuspended MerchantStatus = "suspended"
)

// Merchant is an API consumer: the business system that integrates with
// openpay.id instead of talking to each gateway directly.
type Merchant struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	APIKeyHash    string         `json:"-"`
	APIKeyPrefix  string         `json:"api_key_prefix"`
	CallbackURL   string         `json:"callback_url"`
	WebhookSecret string         `json:"-"`
	Routing       RoutingConfig  `json:"routing"`
	Status        MerchantStatus `json:"status"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// Active reports whether the merchant is allowed to transact.
func (m *Merchant) Active() bool { return m.Status == MerchantActive }

// Routing strategy names understood by the core engine.
const (
	// StrategyPriority always picks the lowest priority number first, falling
	// back to the next provider on failure.
	StrategyPriority = "priority"
	// StrategyRoundRobin rotates the starting provider across requests.
	StrategyRoundRobin = "round_robin"
)

// RoutingConfig describes how the core engine selects a provider for a
// merchant. It is stored as JSON alongside the merchant.
type RoutingConfig struct {
	// Strategy is one of the Strategy* constants. Empty means StrategyPriority.
	Strategy string `json:"strategy"`
	// Providers is the ordered candidate list. Order is meaningful.
	Providers []RoutingProvider `json:"providers"`
}

// MarshalJSON emits an empty provider list as [] so console consumers never
// see a null list from a freshly created merchant (JSON round-trip in
// JavaScript treats the two differently).
func (c RoutingConfig) MarshalJSON() ([]byte, error) {
	type plain RoutingConfig
	p := plain(c)
	if p.Providers == nil {
		p.Providers = []RoutingProvider{}
	}
	return json.Marshal(p)
}

// RoutingProvider is a single provider entry in a routing configuration.
type RoutingProvider struct {
	// Name matches [provider.Provider.Name].
	Name string `json:"name"`
	// Priority orders candidates: lower value wins. Ignored by round_robin.
	Priority int `json:"priority"`
	// Weight is reserved for weighted strategies.
	Weight int `json:"weight,omitempty"`
	// Enabled lets a merchant disable a provider without removing it.
	Enabled bool `json:"enabled"`
}
