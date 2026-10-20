// Package memory provides in-memory implementations of the repository
// interfaces. It is intended for tests and local experiments: it lets the core
// engine and API layer run without PostgreSQL.
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/pkg/provider"
)

// Store holds all in-memory state behind a single mutex.
type Store struct {
	mu              sync.RWMutex
	transactions    map[string]*domain.Transaction
	merchants       map[string]*domain.Merchant
	deliveries      map[string]*domain.WebhookDelivery
	apiKeys         map[string]*domain.APIKey
	adminUsers      map[string]*domain.AdminUser
	adminSessions   map[string]*domain.AdminSession
	auditEntries    []*domain.AuditEntry
	inboundEvents   map[string]*domain.InboundEvent
	providerConfigs map[string]*domain.ProviderConfig
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		transactions:    make(map[string]*domain.Transaction),
		merchants:       make(map[string]*domain.Merchant),
		deliveries:      make(map[string]*domain.WebhookDelivery),
		apiKeys:         make(map[string]*domain.APIKey),
		adminUsers:      make(map[string]*domain.AdminUser),
		adminSessions:   make(map[string]*domain.AdminSession),
		inboundEvents:   make(map[string]*domain.InboundEvent),
		providerConfigs: make(map[string]*domain.ProviderConfig),
	}
}

// Transactions returns the transaction repository view.
func (s *Store) Transactions() repository.TransactionRepository { return &txRepo{store: s} }

// Merchants returns the merchant repository view.
func (s *Store) Merchants() repository.MerchantRepository { return &merchantRepo{store: s} }

// Webhooks returns the webhook delivery repository view.
func (s *Store) Webhooks() repository.WebhookDeliveryRepository { return &webhookRepo{store: s} }

// APIKeys returns the API key repository view.
func (s *Store) APIKeys() repository.APIKeyRepository { return &apiKeyRepo{store: s} }

// AdminUsers returns the admin user repository view.
func (s *Store) AdminUsers() repository.AdminUserRepository { return &adminUserRepo{store: s} }

// AdminSessions returns the admin session repository view.
func (s *Store) AdminSessions() repository.AdminSessionRepository { return &adminSessionRepo{store: s} }

// Audit returns the audit log repository view.
func (s *Store) Audit() repository.AuditRepository { return &auditRepo{store: s} }

// Inbound returns the inbound event repository view.
func (s *Store) Inbound() repository.InboundEventRepository { return &inboundRepo{store: s} }

// ProviderConfigs returns the provider config repository view.
func (s *Store) ProviderConfigs() repository.ProviderConfigRepository {
	return &providerConfigRepo{store: s}
}

// AdminStats returns the aggregate store view.
func (s *Store) AdminStats() repository.AdminStore { return &adminStatsRepo{store: s} }

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
		if f.Environment != "" && tx.Environment != f.Environment {
			continue
		}
		if f.ReferenceID != "" && tx.ReferenceID != f.ReferenceID {
			continue
		}
		if f.ProviderName != "" && tx.ProviderName != f.ProviderName {
			continue
		}
		if needle := strings.ToLower(f.Search); needle != "" &&
			!strings.Contains(strings.ToLower(tx.ReferenceID), needle) &&
			!strings.Contains(strings.ToLower(tx.ProviderReference), needle) {
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
	// Only real credentials conflict; a console-created merchant has no
	// legacy key (empty hash) and must coexist with other such merchants.
	if m.APIKeyHash != "" {
		for _, existing := range r.store.merchants {
			if existing.APIKeyHash == m.APIKeyHash {
				return domain.ErrConflict
			}
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

func (r *webhookRepo) List(_ context.Context, f repository.DeliveryFilter) ([]*domain.WebhookDelivery, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var out []*domain.WebhookDelivery
	for _, d := range r.store.deliveries {
		if f.Environment != "" && d.Environment != f.Environment {
			continue
		}
		if f.Status != "" && d.Status != f.Status {
			continue
		}
		clone := *d
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return paginate(out, f.Limit, f.Offset), nil
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

// paginate applies limit/offset to a result slice.
func paginate(in []*domain.WebhookDelivery, limit, offset int) []*domain.WebhookDelivery {
	if offset > 0 {
		if offset >= len(in) {
			return nil
		}
		in = in[offset:]
	}
	if limit > 0 && len(in) > limit {
		in = in[:limit]
	}
	return in
}

// --- api keys --------------------------------------------------------------

type apiKeyRepo struct{ store *Store }

func (r *apiKeyRepo) Create(_ context.Context, k *domain.APIKey) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for _, existing := range r.store.apiKeys {
		if existing.KeyHash == k.KeyHash {
			return domain.ErrConflict
		}
	}
	clone := *k
	r.store.apiKeys[k.ID] = &clone
	return nil
}

func (r *apiKeyRepo) Update(_ context.Context, k *domain.APIKey) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, ok := r.store.apiKeys[k.ID]; !ok {
		return domain.ErrNotFound
	}
	clone := *k
	r.store.apiKeys[k.ID] = &clone
	return nil
}

func (r *apiKeyRepo) GetByID(_ context.Context, id string) (*domain.APIKey, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	k, ok := r.store.apiKeys[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	clone := *k
	return &clone, nil
}

func (r *apiKeyRepo) GetByHash(_ context.Context, hash string) (*domain.APIKey, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	for _, k := range r.store.apiKeys {
		if k.KeyHash == hash {
			clone := *k
			return &clone, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *apiKeyRepo) List(_ context.Context, f repository.APIKeyFilter) ([]*domain.APIKey, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var out []*domain.APIKey
	for _, k := range r.store.apiKeys {
		if f.MerchantID != "" && k.MerchantID != f.MerchantID {
			continue
		}
		if f.Environment != "" && k.Environment != f.Environment {
			continue
		}
		clone := *k
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return paginateKeys(out, f.Limit, f.Offset), nil
}

func paginateKeys(in []*domain.APIKey, limit, offset int) []*domain.APIKey {
	if offset > 0 {
		if offset >= len(in) {
			return nil
		}
		in = in[offset:]
	}
	if limit > 0 && len(in) > limit {
		in = in[:limit]
	}
	return in
}

func (r *apiKeyRepo) EnvironmentsOfMerchant(_ context.Context, merchantID string) ([]string, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	set := map[string]bool{}
	for _, k := range r.store.apiKeys {
		if k.MerchantID == merchantID && k.Status == domain.APIKeyActive {
			set[k.Environment] = true
		}
	}
	out := make([]string, 0, len(set))
	for env := range set {
		out = append(out, env)
	}
	sort.Strings(out)
	return out, nil
}

// --- admin users -----------------------------------------------------------

type adminUserRepo struct{ store *Store }

func (r *adminUserRepo) Create(_ context.Context, u *domain.AdminUser) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for _, existing := range r.store.adminUsers {
		if existing.Email == u.Email {
			return domain.ErrConflict
		}
	}
	clone := *u
	r.store.adminUsers[u.ID] = &clone
	return nil
}

func (r *adminUserRepo) CreateFirst(_ context.Context, u *domain.AdminUser) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if len(r.store.adminUsers) != 0 {
		return domain.ErrConflict
	}
	clone := *u
	r.store.adminUsers[u.ID] = &clone
	return nil
}

func (r *adminUserRepo) Update(_ context.Context, u *domain.AdminUser) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, ok := r.store.adminUsers[u.ID]; !ok {
		return domain.ErrNotFound
	}
	clone := *u
	r.store.adminUsers[u.ID] = &clone
	return nil
}

func (r *adminUserRepo) GetByID(_ context.Context, id string) (*domain.AdminUser, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	u, ok := r.store.adminUsers[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	clone := *u
	return &clone, nil
}

func (r *adminUserRepo) GetByEmail(_ context.Context, email string) (*domain.AdminUser, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	for _, u := range r.store.adminUsers {
		if u.Email == email {
			clone := *u
			return &clone, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *adminUserRepo) List(_ context.Context) ([]*domain.AdminUser, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var out []*domain.AdminUser
	for _, u := range r.store.adminUsers {
		clone := *u
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (r *adminUserRepo) Count(_ context.Context) (int64, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	return int64(len(r.store.adminUsers)), nil
}

// --- admin sessions --------------------------------------------------------

type adminSessionRepo struct{ store *Store }

func (r *adminSessionRepo) Create(_ context.Context, s *domain.AdminSession) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	clone := *s
	r.store.adminSessions[s.TokenHash] = &clone
	return nil
}

func (r *adminSessionRepo) Get(_ context.Context, tokenHash string, now time.Time) (*domain.AdminSession, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	s, ok := r.store.adminSessions[tokenHash]
	if !ok || s.Expired(now) {
		return nil, domain.ErrNotFound
	}
	clone := *s
	return &clone, nil
}

func (r *adminSessionRepo) Delete(_ context.Context, tokenHash string) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	delete(r.store.adminSessions, tokenHash)
	return nil
}

func (r *adminSessionRepo) DeleteForUser(_ context.Context, userID string) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for hash, s := range r.store.adminSessions {
		if s.UserID == userID {
			delete(r.store.adminSessions, hash)
		}
	}
	return nil
}

// --- audit log -------------------------------------------------------------

type auditRepo struct{ store *Store }

func (r *auditRepo) Create(_ context.Context, e *domain.AuditEntry) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	clone := *e
	r.store.auditEntries = append(r.store.auditEntries, &clone)
	return nil
}

func (r *auditRepo) List(_ context.Context, f repository.AuditFilter) ([]*domain.AuditEntry, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	// Newest first: iterate the append-only slice backwards.
	var out []*domain.AuditEntry
	needle := strings.ToLower(f.Search)
	for i := len(r.store.auditEntries) - 1; i >= 0; i-- {
		e := r.store.auditEntries[i]
		if needle != "" &&
			!strings.Contains(strings.ToLower(e.Actor), needle) &&
			!strings.Contains(strings.ToLower(e.Action), needle) &&
			!strings.Contains(strings.ToLower(e.Target), needle) {
			continue
		}
		clone := *e
		out = append(out, &clone)
	}
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return nil, nil
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// --- inbound events --------------------------------------------------------

type inboundRepo struct{ store *Store }

func (r *inboundRepo) Create(_ context.Context, e *domain.InboundEvent) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	clone := *e
	r.store.inboundEvents[e.ID] = &clone
	return nil
}

func (r *inboundRepo) GetByID(_ context.Context, id string) (*domain.InboundEvent, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	e, ok := r.store.inboundEvents[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	clone := *e
	return &clone, nil
}

func (r *inboundRepo) Update(_ context.Context, e *domain.InboundEvent) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	stored, ok := r.store.inboundEvents[e.ID]
	if !ok {
		return domain.ErrNotFound
	}
	stored.Environment = e.Environment
	stored.TransactionID = e.TransactionID
	stored.Applied = e.Applied
	stored.Note = e.Note
	return nil
}

func (r *inboundRepo) List(_ context.Context, f repository.InboundFilter) ([]*domain.InboundEvent, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var out []*domain.InboundEvent
	for _, e := range r.store.inboundEvents {
		// Unknown-environment callbacks stay visible under every env filter,
		// mirroring the PostgreSQL implementation.
		if f.Environment != "" && e.Environment != "" && e.Environment != f.Environment {
			continue
		}
		if f.TransactionID != "" && e.TransactionID != f.TransactionID {
			continue
		}
		clone := *e
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceivedAt.After(out[j].ReceivedAt) })
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return nil, nil
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// --- provider configs ------------------------------------------------------

type providerConfigRepo struct{ store *Store }

func (r *providerConfigRepo) Upsert(_ context.Context, pc *domain.ProviderConfig) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	cfg := make(map[string]string, len(pc.Config))
	for k, v := range pc.Config {
		cfg[k] = v
	}
	r.store.providerConfigs[pc.Provider+"/"+pc.Environment] = &domain.ProviderConfig{
		Provider:    pc.Provider,
		Environment: pc.Environment,
		Config:      cfg,
		UpdatedAt:   pc.UpdatedAt,
		UpdatedBy:   pc.UpdatedBy,
	}
	return nil
}

func (r *providerConfigRepo) Get(_ context.Context, provider, environment string) (*domain.ProviderConfig, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	pc, ok := r.store.providerConfigs[provider+"/"+environment]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cfg := make(map[string]string, len(pc.Config))
	for k, v := range pc.Config {
		cfg[k] = v
	}
	return &domain.ProviderConfig{
		Provider:    pc.Provider,
		Environment: pc.Environment,
		Config:      cfg,
		UpdatedAt:   pc.UpdatedAt,
		UpdatedBy:   pc.UpdatedBy,
	}, nil
}

func (r *providerConfigRepo) List(_ context.Context) ([]*domain.ProviderConfig, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var out []*domain.ProviderConfig
	for _, pc := range r.store.providerConfigs {
		cfg := make(map[string]string, len(pc.Config))
		for k, v := range pc.Config {
			cfg[k] = v
		}
		out = append(out, &domain.ProviderConfig{
			Provider:    pc.Provider,
			Environment: pc.Environment,
			Config:      cfg,
			UpdatedAt:   pc.UpdatedAt,
			UpdatedBy:   pc.UpdatedBy,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Environment < out[j].Environment
	})
	return out, nil
}

func (r *providerConfigRepo) Delete(_ context.Context, provider, environment string) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	key := provider + "/" + environment
	if _, ok := r.store.providerConfigs[key]; !ok {
		return domain.ErrNotFound
	}
	delete(r.store.providerConfigs, key)
	return nil
}

// --- aggregates (admin store) ----------------------------------------------

type adminStatsRepo struct{ store *Store }

func (r *adminStatsRepo) MerchantsWithStats(_ context.Context, search string) ([]*repository.MerchantListing, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	now := time.Now()
	var out []*repository.MerchantListing
	needle := strings.ToLower(search)
	for _, m := range r.store.merchants {
		if needle != "" && !strings.Contains(strings.ToLower(m.Name), needle) {
			continue
		}
		listing := &repository.MerchantListing{Merchant: func() *domain.Merchant { c := *m; return &c }()}
		for _, k := range r.store.apiKeys {
			if k.MerchantID == m.ID && k.Status == domain.APIKeyActive {
				listing.ActiveKeys++
			}
		}
		for _, tx := range r.store.transactions {
			if tx.MerchantID == m.ID && tx.CreatedAt.After(now.Add(-30*24*time.Hour)) {
				listing.Transactions30d++
			}
		}
		out = append(out, listing)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Merchant.CreatedAt.After(out[j].Merchant.CreatedAt)
	})
	return out, nil
}

func (r *adminStatsRepo) Overview(_ context.Context, environment string) (*repository.OverviewStats, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	now := time.Now()
	out := &repository.OverviewStats{
		CreatedPerHour: make([]int64, 24),
		FailedPerHour:  make([]int64, 24),
	}
	scoped := func(env string) bool {
		return environment == "" || env == "" || env == environment
	}

	counts := map[string]int64{}
	for _, tx := range r.store.transactions {
		if !scoped(tx.Environment) {
			continue
		}
		counts[string(tx.Status)]++
		out.TotalTransactions++

		// Hourly buckets, oldest first: hoursAgo 23 -> index 0, hoursAgo 0 -> 23.
		if hoursAgo := int(now.Sub(tx.CreatedAt) / time.Hour); tx.CreatedAt.After(now.Add(-24*time.Hour)) && hoursAgo >= 0 && hoursAgo < 24 {
			bucket := 23 - hoursAgo
			out.CreatedPerHour[bucket]++
			if tx.Status == provider.StatusFailed {
				out.FailedPerHour[bucket]++
			}
		}
	}
	for _, status := range []string{
		"paid", "pending", "expired", "failed", "cancelled", "refunded", "unknown",
	} {
		out.Statuses = append(out.Statuses, repository.StatusCount{
			Status: provider.Status(status),
			Count:  counts[status],
		})
	}

	// Per-provider rollup over the last 30 days.
	index := map[string]int{}
	for _, tx := range r.store.transactions {
		if !scoped(tx.Environment) {
			continue
		}
		if tx.CreatedAt.Before(now.Add(-30 * 24 * time.Hour)) {
			continue
		}
		i, ok := index[tx.ProviderName]
		if !ok {
			i = len(out.Providers)
			index[tx.ProviderName] = i
			out.Providers = append(out.Providers, repository.ProviderReliability{
				Provider: tx.ProviderName,
			})
		}
		out.Providers[i].Payments++
		if tx.Status == provider.StatusFailed {
			out.Providers[i].Failed++
		}
	}

	for _, d := range r.store.deliveries {
		if !scoped(d.Environment) {
			continue
		}
		switch d.Status {
		case domain.DeliveryDelivered:
			out.DeliveredDeliveries++
		case domain.DeliveryPending:
			out.PendingDeliveries++
		case domain.DeliveryFailed:
			out.FailedDeliveries++
		}
	}
	return out, nil
}
