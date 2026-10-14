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
	ReferenceID  string
	ProviderName string
	Statuses     []provider.Status
	Method       provider.Method
	From         *time.Time
	To           *time.Time
	Limit        int
	Offset       int
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
	// GetByProviderReference loads by the gateway's transaction identifier.
	GetByProviderReference(ctx context.Context, providerName, providerReference string) (*domain.Transaction, error)
	// GetByProviderAndReference loads by gateway name + merchant reference. It
	// backs webhook correlation for gateways that echo the order id but not
	// their own transaction id.
	GetByProviderAndReference(ctx context.Context, providerName, referenceID string) (*domain.Transaction, error)
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
}
