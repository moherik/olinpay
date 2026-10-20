// Package repository defines the persistence contracts used by the core
// engine. The interfaces live here (not next to the SQL) so that the engine can
// be tested against in-memory fakes and so that swapping PostgreSQL for another
// store is a matter of providing a new implementation.
package repository

import (
	"context"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/pkg/provider"
)

// TransactionFilter narrows a transaction listing query.
type TransactionFilter struct {
	MerchantID   string
	Environment  string
	ReferenceID  string
	ProviderName string
	// Search matches loosely against reference_id and provider_reference.
	Search   string
	Statuses []provider.Status
	Method   provider.Method
	From     *time.Time
	To       *time.Time
	Limit    int
	Offset   int
}

// TransactionRepository persists payment transactions.
type TransactionRepository interface {
	// Create inserts a new transaction. It returns domain.ErrConflict when the
	// (merchant_id, reference_id) pair already exists.
	Create(ctx context.Context, tx *domain.Transaction) error
	// Update persists mutable fields of an existing transaction.
	Update(ctx context.Context, tx *domain.Transaction) error
	// GetByID loads a transaction by its internal identifier.
	GetByID(ctx context.Context, id string) (*domain.Transaction, error)
	// GetByReference loads by the merchant-scoped reference identifier.
	GetByReference(ctx context.Context, merchantID, referenceID string) (*domain.Transaction, error)
	// GetByProviderReference loads by adapter name, gateway transaction identifier,
	// and environment.
	GetByProviderReference(ctx context.Context, providerName, providerReference, environment string) (*domain.Transaction, error)
	// GetByProviderAndReference loads by adapter name + merchant reference + environment. It
	// backs webhook correlation for gateways that echo the order id but not
	// their own transaction id.
	GetByProviderAndReference(ctx context.Context, providerName, referenceID, environment string) (*domain.Transaction, error)
	// List returns transactions matching the filter, newest first.
	List(ctx context.Context, filter TransactionFilter) ([]*domain.Transaction, error)
}

// MerchantRepository persists merchant (API consumer) accounts.
type MerchantRepository interface {
	// Create inserts a new merchant. It returns domain.ErrConflict when the
	// API key hash is already used.
	Create(ctx context.Context, m *domain.Merchant) error
	// Update persists mutable merchant fields (name, callback URL, routing...).
	Update(ctx context.Context, m *domain.Merchant) error
	// GetByID loads a merchant by identifier.
	GetByID(ctx context.Context, id string) (*domain.Merchant, error)
	// GetByAPIKeyHash resolves a merchant from a hashed API credential.
	GetByAPIKeyHash(ctx context.Context, hash string) (*domain.Merchant, error)
	// List returns merchants, newest first.
	List(ctx context.Context, limit, offset int) ([]*domain.Merchant, error)
}

// MerchantSettingsRepository persists settings that affect payment behavior
// without adding them to the merchant's core identity record.
type MerchantSettingsRepository interface {
	// Get returns an empty settings object when the merchant has no saved row.
	Get(ctx context.Context, merchantID string) (*domain.MerchantSettings, error)
	// Upsert creates or replaces the merchant's settings.
	Upsert(ctx context.Context, settings *domain.MerchantSettings) error
}

// MerchantListing is a merchant joined with the counts the console list shows.
type MerchantListing struct {
	Merchant        *domain.Merchant
	ActiveKeys      int
	Transactions30d int
}

// AdminStore answers the aggregate queries the admin console is built on.
// These are cross-entity statistics that no single entity repository owns; the
// PostgreSQL implementation expresses them as SQL, the in-memory fallback
// counts in Go.
type AdminStore interface {
	// MerchantsWithStats lists merchants with the API-key and 30-day
	// transaction counts shown on the merchants page.
	MerchantsWithStats(ctx context.Context, search string) ([]*MerchantListing, error)
	// Overview holds the aggregate numbers behind the console overview page.
	Overview(ctx context.Context, environment string) (*OverviewStats, error)
}

// OverviewStats aggregates transaction and delivery state for one environment
// (empty string = all environments).
type OverviewStats struct {
	// TotalTransactions counts every transaction in scope.
	TotalTransactions int64
	// StatusBreakdown maps unified status -> count. The full unified status
	// set is always materialized, zero-filled, in the unified status order.
	Statuses []StatusCount
	// CreatedPerHour has 24 buckets, oldest first, counting payments created
	// in the last 24 hours.
	CreatedPerHour []int64
	// FailedPerHour parallels CreatedPerHour for failed payments.
	FailedPerHour []int64
	// Deliveries holds outbound webhook delivery counts by state.
	DeliveredDeliveries int64
	PendingDeliveries   int64
	FailedDeliveries    int64
	// ProviderReliability counts payments and failed payments per provider.
	Providers []ProviderReliability
}

// StatusCount is one row of the status breakdown.
type StatusCount struct {
	Status provider.Status
	Count  int64
}

// ProviderReliability is one row of the provider reliability table.
type ProviderReliability struct {
	Provider string
	Payments int64
	Failed   int64
}

// WebhookDeliveryRepository persists the outbound merchant notification queue.
type WebhookDeliveryRepository interface {
	// Create enqueues a delivery.
	Create(ctx context.Context, d *domain.WebhookDelivery) error
	// Update persists delivery progress (attempts, status, next attempt).
	Update(ctx context.Context, d *domain.WebhookDelivery) error
	// GetByID loads a delivery by identifier.
	GetByID(ctx context.Context, id string) (*domain.WebhookDelivery, error)
	// Due returns up to limit pending deliveries whose next attempt is due.
	Due(ctx context.Context, now time.Time, limit int) ([]*domain.WebhookDelivery, error)
	// List returns deliveries matching the filter, newest first. It powers the
	// console's outbound deliveries tab.
	List(ctx context.Context, f DeliveryFilter) ([]*domain.WebhookDelivery, error)
}

// DeliveryFilter narrows a webhook delivery listing query.
type DeliveryFilter struct {
	Environment string
	Status      domain.DeliveryStatus
	Limit       int
	Offset      int
}
