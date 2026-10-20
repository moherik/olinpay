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

// MerchantSettings holds merchant-owned redirect behavior that applies to all
// payment requests unless the merchant explicitly overrides it per request.
type MerchantSettings struct {
	MerchantID       string    `json:"merchant_id"`
	ReturnURL        string    `json:"return_url"`
	SuccessReturnURL string    `json:"success_return_url"`
	FailureReturnURL string    `json:"failure_return_url"`
	UpdatedAt        time.Time `json:"updated_at"`
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
	// Providers is the ordered candidate list. Order is meaningful. It doubles
	// as the fallback chain when conditional rules are configured.
	Providers []RoutingProvider `json:"providers"`
	// Rules are conditional routes evaluated in order. The first rule whose
	// condition matches prepends its provider chain ahead of Providers for
	// that request; the base chain still serves as the universal fallback.
	Rules []RoutingRule `json:"rules"`
	// Enrich applies request modifications before rule evaluation, so
	// metadata conditions can read enriched values. It corresponds to modify
	// nodes on the main path right after the entry.
	Enrich []RoutingModify `json:"enrich,omitempty"`
	// Reject responds with a custom error when the fallback chain is
	// exhausted (all providers declined or nothing was eligible). It
	// corresponds to a respond node at the end of the fallback chain.
	Reject *RoutingReject `json:"reject,omitempty"`
}

// MarshalJSON emits an empty provider or rule list as [] so console consumers
// never see a null list from a freshly created merchant (JSON round-trip in
// JavaScript treats the two differently).
func (c RoutingConfig) MarshalJSON() ([]byte, error) {
	type plain RoutingConfig
	p := plain(c)
	if p.Providers == nil {
		p.Providers = []RoutingProvider{}
	}
	if p.Rules == nil {
		p.Rules = []RoutingRule{}
	}
	return json.Marshal(p)
}

// Condition fields and operators accepted in RoutingRule.Condition. Amount
// compares the request's minor units as an integer; the remaining fields
// compare strings. A "metadata.<key>" field compares request metadata.
const (
	ConditionFieldAmount    = "amount"
	ConditionFieldMethod    = "method"
	ConditionFieldChannel   = "channel"
	ConditionFieldCurrency  = "currency"
	ConditionOperatorEq     = "eq"
	ConditionOperatorNotEq  = "neq"
	ConditionOperatorGte    = "gte"
	ConditionOperatorLte    = "lte"
	ConditionMetadataPrefix = "metadata:"
)

// RoutingRule conditionally overrides the provider chain for matching
// requests.
type RoutingRule struct {
	// Condition is evaluated against the payment request.
	Condition RoutingCondition `json:"condition"`
	// Providers is the ordered provider chain for matching requests.
	Providers []string `json:"providers"`
	// Apply modifies the request before it reaches the rule's provider chain.
	Apply []RoutingModify `json:"apply,omitempty"`
	// Reject responds with a custom error for matching requests instead of
	// routing to a provider. Mutually exclusive with Providers.
	Reject *RoutingReject `json:"reject,omitempty"`
}

// RoutingReject is the custom response a routing rule (or an exhausted
// fallback chain) answers with instead of dispatching to a provider.
type RoutingReject struct {
	// Status is the HTTP status the payment API answers with, 400..599.
	Status int `json:"status"`
	// Code is the machine-readable error code (e.g. "amount_not_supported").
	Code string `json:"code"`
	// Message is the human-readable explanation.
	Message string `json:"message"`
}

// Modify operations accepted in RoutingModify.
const (
	ModifyOpSetMetadata    = "set_metadata"
	ModifyOpSetDescription = "set_description"
)

// RoutingModify is one request mutation applied by routing before the payment
// reaches a provider (or before rule evaluation, for enrichment).
type RoutingModify struct {
	// Op is set_metadata or set_description.
	Op string `json:"op"`
	// Key is the metadata key for set_metadata.
	Key string `json:"key,omitempty"`
	// Value is the value to set.
	Value string `json:"value"`
}

// RoutingCondition is the comparison a rule performs. Unknown fields or
// operators make the rule inert (the engine skips it), and the admin API
// validates the pair on save so a console typo cannot silently no-op.
type RoutingCondition struct {
	// Field is amount, method, channel, currency, or "metadata:<key>".
	Field string `json:"field"`
	// Operator is eq, neq, gte or lte; gte/lte are only valid on amount.
	Operator string `json:"operator"`
	// Value is the comparison target; for amount it parses as an integer
	// using the create-payment contract's unit (IDR: integer rupiah).
	Value string `json:"value"`
}

// RoutingProvider is a single provider entry in a routing configuration.
type RoutingProvider struct {
	// Name identifies the registered provider instance, which may include an
	// environment suffix such as "winpay.sandbox".
	Name string `json:"name"`
	// Priority orders candidates: lower value wins. Ignored by round_robin.
	Priority int `json:"priority"`
	// Weight is reserved for weighted strategies.
	Weight int `json:"weight,omitempty"`
	// Enabled lets a merchant disable a provider without removing it.
	Enabled bool `json:"enabled"`
}
