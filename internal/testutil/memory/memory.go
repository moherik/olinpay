// Package memory provides in-memory implementations of the repository
// interfaces. It is intended for tests and local experiments: it lets the core
// engine and API layer run without PostgreSQL.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/pkg/provider"
)

// Store holds all in-memory state behind a single mutex.
type Store struct {
	mu           sync.RWMutex
	transactions map[string]*domain.Transaction
	merchants    map[string]*domain.Merchant
	deliveries   map[string]*domain.WebhookDelivery
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		transactions: make(map[string]*domain.Transaction),
		merchants:    make(map[string]*domain.Merchant),
		deliveries:   make(map[string]*domain.WebhookDelivery),
	}
}

// Transactions returns the transaction repository view.
func (s *Store) Transactions() repository.TransactionRepository { return &txRepo{store: s} }

// Merchants returns the merchant repository view.
func (s *Store) Merchants() repository.MerchantRepository { return &merchantRepo{store: s} }

// Webhooks returns the webhook delivery repository view.
func (s *Store) Webhooks() repository.WebhookDeliveryRepository { return &webhookRepo{store: s} }

// --- transactions ----------------------------------------------------------

type txRepo struct{ store *Store }

func (r *txRepo) Create(_ context.Context, tx *domain.Transaction) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for _, existing := range r.store.transactions {
		if existing.MerchantID == tx.MerchantID && existing.ReferenceID == tx.ReferenceID {
			return domain.ErrConflict
		}
	}
	clone := *tx
	r.store.transactions[tx.ID] = &clone
	return nil
}

func (r *txRepo) Update(_ context.Context, tx *domain.Transaction) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	existing, ok := r.store.transactions[tx.ID]
	if !ok {
		return domain.ErrNotFound
	}
	clone := *tx
	r.store.transactions[tx.ID] = &clone
	_ = existing
	return nil
}

func (r *txRepo) GetByID(_ context.Context, id string) (*domain.Transaction, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	tx, ok := r.store.transactions[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	clone := *tx
	return &clone, nil
}

func (r *txRepo) GetByReference(_ context.Context, merchantID, referenceID string) (*domain.Transaction, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	for _, tx := range r.store.transactions {
		if tx.MerchantID == merchantID && tx.ReferenceID == referenceID {
			clone := *tx
			return &clone, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *txRepo) GetByProviderReference(_ context.Context, providerName, providerReference string) (*domain.Transaction, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	for _, tx := range r.store.transactions {
		if tx.ProviderName == providerName && tx.ProviderReference == providerReference {
			clone := *tx
			return &clone, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *txRepo) GetByProviderAndReference(_ context.Context, providerName, referenceID string) (*domain.Transaction, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	for _, tx := range r.store.transactions {
		if tx.ProviderName == providerName && tx.ReferenceID == referenceID {
			clone := *tx
			return &clone, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *txRepo) List(_ context.Context, f repository.TransactionFilter) ([]*domain.Transaction, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var out []*domain.Transaction
	for _, tx := range r.store.transactions {
		if f.MerchantID != "" && tx.MerchantID != f.MerchantID {
			continue
		}
		if f.ReferenceID != "" && tx.ReferenceID != f.ReferenceID {
			continue
		}
		if f.ProviderName != "" && tx.ProviderName != f.ProviderName {
			continue
		}
		if f.Method != "" && tx.Method != f.Method {
			continue
		}
		if len(f.Statuses) > 0 && !containsStatus(f.Statuses, tx.Status) {
			continue
		}
		clone := *tx
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if f.Offset > 0 && f.Offset < len(out) {
		out = out[f.Offset:]
	} else if f.Offset >= len(out) {
		out = nil
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func containsStatus(list []provider.Status, want provider.Status) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// --- merchants -------------------------------------------------------------

type merchantRepo struct{ store *Store }

func (r *merchantRepo) Create(_ context.Context, m *domain.Merchant) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for _, existing := range r.store.merchants {
		if existing.APIKeyHash == m.APIKeyHash {
			return domain.ErrConflict
		}
	}
	clone := *m
	r.store.merchants[m.ID] = &clone
	return nil
}

func (r *merchantRepo) Update(_ context.Context, m *domain.Merchant) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, ok := r.store.merchants[m.ID]; !ok {
		return domain.ErrNotFound
	}
	clone := *m
	r.store.merchants[m.ID] = &clone
	return nil
}

func (r *merchantRepo) GetByID(_ context.Context, id string) (*domain.Merchant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	m, ok := r.store.merchants[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	clone := *m
	return &clone, nil
}

func (r *merchantRepo) GetByAPIKeyHash(_ context.Context, hash string) (*domain.Merchant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	for _, m := range r.store.merchants {
		if m.APIKeyHash == hash {
			clone := *m
			return &clone, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *merchantRepo) List(_ context.Context, limit, offset int) ([]*domain.Merchant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var out []*domain.Merchant
	for _, m := range r.store.merchants {
		clone := *m
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if offset > 0 && offset < len(out) {
		out = out[offset:]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// --- webhook deliveries ----------------------------------------------------

type webhookRepo struct{ store *Store }

func (r *webhookRepo) Create(_ context.Context, d *domain.WebhookDelivery) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	clone := *d
	clone.Payload = append([]byte(nil), d.Payload...)
	r.store.deliveries[d.ID] = &clone
	return nil
}

func (r *webhookRepo) Update(_ context.Context, d *domain.WebhookDelivery) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, ok := r.store.deliveries[d.ID]; !ok {
		return domain.ErrNotFound
	}
	clone := *d
	clone.Payload = append([]byte(nil), d.Payload...)
	r.store.deliveries[d.ID] = &clone
	return nil
}

func (r *webhookRepo) GetByID(_ context.Context, id string) (*domain.WebhookDelivery, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	d, ok := r.store.deliveries[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	clone := *d
	return &clone, nil
}

func (r *webhookRepo) Due(_ context.Context, now time.Time, limit int) ([]*domain.WebhookDelivery, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var out []*domain.WebhookDelivery
	for _, d := range r.store.deliveries {
		if d.Status != domain.DeliveryPending {
			continue
		}
		if d.NextAttemptAt != nil && d.NextAttemptAt.After(now) {
			continue
		}
		clone := *d
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
