// Package core is the heart of openpay.id: the unified payment processor.
//
// It owns three responsibilities:
//
//  1. translating an API request into a provider-agnostic [provider.Provider]
//     call and back;
//  2. routing each request to a provider using the merchant's configured
//     strategy, with ordered failover across candidates;
//  3. applying inbound gateway events (webhooks) to the persisted transaction
//     and emitting an outbound notification to the merchant system.
//
// The engine is deliberately ignorant of HTTP and SQL: it depends on the
// [provider.Registry] and on the repository interfaces only.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/id"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/pkg/provider"
)

// Notifier delivers outbound notifications to a merchant system. The webhook
// dispatcher implements it; the engine only knows this small surface.
type Notifier interface {
	// Notify enqueues a notification describing the transaction's new state.
	Notify(ctx context.Context, tx *domain.Transaction) error
}

// Deps are the engine's collaborators.
type Deps struct {
	Registry         *provider.Registry
	Router           *Router
	Transactions     repository.TransactionRepository
	Merchants        repository.MerchantRepository
	MerchantSettings repository.MerchantSettingsRepository
	Notifier         Notifier
	Logger           *slog.Logger
}

// Engine is the unified payment processor.
type Engine struct {
	registry     *provider.Registry
	router       *Router
	txRepo       repository.TransactionRepository
	merchantRepo repository.MerchantRepository
	settingsRepo repository.MerchantSettingsRepository
	notifier     Notifier
	log          *slog.Logger

	now   func() time.Time
	newID func() string
}

// New constructs an engine from its dependencies.
func New(d Deps) *Engine {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	router := d.Router
	if router == nil {
		router = NewRouter(d.Registry)
	}
	return &Engine{
		registry:     d.Registry,
		router:       router,
		txRepo:       d.Transactions,
		merchantRepo: d.Merchants,
		settingsRepo: d.MerchantSettings,
		notifier:     d.Notifier,
		log:          log,
		now:          time.Now,
		newID:        func() string { return id.NewPrefixed("trx") },
	}
}

// Registry exposes the provider registry (used by the API layer for discovery).
func (e *Engine) Registry() *provider.Registry { return e.registry }

// List returns transactions matching the filter. Callers must scope the filter
// to a single merchant; the API layer always sets MerchantID.
func (e *Engine) List(ctx context.Context, f repository.TransactionFilter) ([]*domain.Transaction, error) {
	return e.txRepo.List(ctx, f)
}

// --- Commands --------------------------------------------------------------

// CreatePaymentCommand is the input to [Engine.CreatePayment].
type CreatePaymentCommand struct {
	MerchantID string
	// Environment is the isolation level the payment belongs to (sandbox or
	// production). It is decided by the API key used, never by the request.
	Environment string
	ReferenceID string
	Amount      provider.Amount
	Currency    provider.Currency
	Method      provider.Method
	Channel     string
	Customer    provider.Customer
	Items       []provider.Item
	Description string
	Expiry      time.Duration
	Metadata    map[string]string
	// ProviderName, when set, bypasses routing and forces a single provider.
	ProviderName string
}

func (c CreatePaymentCommand) validate() error {
	if strings.TrimSpace(c.MerchantID) == "" {
		return domain.Invalid("merchant_id", "is required")
	}
	if strings.TrimSpace(c.ReferenceID) == "" {
		return domain.Invalid("reference_id", "is required")
	}
	if len(c.ReferenceID) > 128 {
		return domain.Invalid("reference_id", "must be at most 128 characters")
	}
	if c.Amount <= 0 {
		return domain.Invalid("amount", "must be greater than zero")
	}
	if c.Environment != "" && !domain.EnvironmentValid(c.Environment) {
		return domain.Invalid("environment", fmt.Sprintf("%q is not a supported environment", c.Environment))
	}
	if !c.Method.IsValid() {
		return domain.Invalid("method", fmt.Sprintf("%q is not a supported payment method", c.Method))
	}
	if c.Expiry < 0 {
		return domain.Invalid("expiry", "must not be negative")
	}
	return nil
}

func (c CreatePaymentCommand) currency() provider.Currency {
	if c.Currency == "" {
		return provider.IDR
	}
	return c.Currency
}

// InquiryQuery is the input to [Engine.Inquiry].
type InquiryQuery struct {
	MerchantID    string
	TransactionID string
	ReferenceID   string
	// Sync forces a live gateway call to refresh the status when true.
	Sync bool
}

// CancelCommand is the input to [Engine.Cancel].
type CancelCommand struct {
	MerchantID    string
	TransactionID string
	ReferenceID   string
	// Reason is "cancel" or "expire" by convention.
	Reason string
}

// --- CreatePayment ---------------------------------------------------------

// CreatePayment creates a payment, persisting the resulting transaction.
//
// It is idempotent on (merchant, reference_id): replaying the same command
// returns the original transaction instead of charging twice.
func (e *Engine) CreatePayment(ctx context.Context, cmd CreatePaymentCommand) (*domain.Transaction, error) {
	if err := cmd.validate(); err != nil {
		return nil, err
	}

	merchant, err := e.loadMerchant(ctx, cmd.MerchantID)
	if err != nil {
		return nil, err
	}
	if !merchant.Active() {
		return nil, fmt.Errorf("%w: merchant is %s", domain.ErrForbidden, merchant.Status)
	}

	// Idempotency check before touching any gateway.
	if existing, err := e.txRepo.GetByReference(ctx, merchant.ID, cmd.ReferenceID); err == nil {
		return existing, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	returnURL := ""
	successReturnURL := ""
	failureReturnURL := ""
	if e.settingsRepo != nil {
		settings, err := e.settingsRepo.Get(ctx, merchant.ID)
		if err != nil {
			return nil, err
		}
		returnURL = settings.ReturnURL
		successReturnURL = settings.SuccessReturnURL
		failureReturnURL = settings.FailureReturnURL
	}

	routeReq := &RouteRequest{
		MerchantID:  merchant.ID,
		Environment: cmd.Environment,
		Method:      cmd.Method,
		Channel:     cmd.Channel,
		Amount:      cmd.Amount,
		Currency:    cmd.currency(),
		Metadata:    cmd.Metadata,
		Description: cmd.Description,
	}
	decision, err := e.candidates(merchant, cmd.ProviderName, routeReq)
	if err != nil {
		return nil, err
	}
	candidates := decision.Candidates

	// The transaction and the gateway request must carry exactly what routing
	// decided to send, so enrichment/modifications land in both.
	effectiveMetadata := decision.Metadata
	if effectiveMetadata == nil {
		effectiveMetadata = cmd.Metadata
	}
	effectiveDescription := decision.Description
	if effectiveDescription == "" {
		effectiveDescription = cmd.Description
	}

	provReq := &provider.CreatePaymentRequest{
		ReferenceID:      cmd.ReferenceID,
		Amount:           cmd.Amount,
		Currency:         cmd.currency(),
		Method:           cmd.Method,
		Channel:          cmd.Channel,
		Customer:         cmd.Customer,
		Items:            cmd.Items,
		Description:      effectiveDescription,
		Expiry:           cmd.Expiry,
		Metadata:         effectiveMetadata,
		ReturnURL:        returnURL,
		SuccessReturnURL: successReturnURL,
		FailureReturnURL: failureReturnURL,
	}

	transactionID := e.newID()
	ctx = provider.WithTraceContext(ctx, provider.TraceContext{MerchantID: merchant.ID, Environment: cmd.Environment, ReferenceID: cmd.ReferenceID, TransactionID: transactionID})
	var traceMu sync.Mutex
	var traces []provider.HTTPTrace
	ctx = provider.WithHTTPActivityRecorder(ctx, func(_ context.Context, trace provider.HTTPTrace) {
		traceMu.Lock()
		traces = append(traces, trace)
		traceMu.Unlock()
	})
	resp, chosen, err := e.attemptCreate(ctx, candidates, provReq)
	if err != nil {
		// A routing-level custom response answers instead of the provider
		// failure when the merchant configured a fallback reject.
		var rejected *domain.RejectedError
		if !errors.As(err, &rejected) && merchant.Routing.Reject != nil {
			return nil, &domain.RejectedError{
				Status:  merchant.Routing.Reject.Status,
				Code:    merchant.Routing.Reject.Code,
				Message: merchant.Routing.Reject.Message,
			}
		}
		return nil, err
	}

	providerName := chosen.Provider.Name()
	tx := e.buildTransaction(merchant, cmd, providerName, resp, effectiveMetadata, effectiveDescription)
	tx.ID = transactionID
	if err := e.txRepo.Create(ctx, tx); err != nil {
		// A concurrent request may have won the idempotency race.
		if errors.Is(err, domain.ErrConflict) {
			if existing, gerr := e.txRepo.GetByReference(ctx, merchant.ID, cmd.ReferenceID); gerr == nil {
				return existing, nil
			}
		}
		return nil, err
	}
	if recorder := provider.CurrentHTTPActivityRecorder(); recorder != nil {
		traceMu.Lock()
		captured := append([]provider.HTTPTrace(nil), traces...)
		traceMu.Unlock()
		for _, trace := range captured {
			recorder(ctx, trace)
		}
	}

	e.log.Info("payment created",
		"transaction_id", tx.ID,
		"merchant_id", tx.MerchantID,
		"provider", tx.ProviderName,
		"method", tx.Method,
		"status", tx.Status,
	)

	// Instant settlements (some e-wallet/direct-debit flows) are surfaced now.
	if tx.IsTerminal() {
		e.notify(ctx, tx)
	}
	return tx, nil
}

func (e *Engine) attemptCreate(ctx context.Context, candidates []Candidate, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, Candidate, error) {
	var failures []error
	for _, cand := range candidates {
		resp, err := cand.Provider.CreatePayment(ctx, req)
		if err != nil {
			e.log.Warn("provider create failed, trying next",
				"provider", candidateName(cand), "reference_id", req.ReferenceID, "error", err.Error())
			failures = append(failures, err)
			continue
		}
		return resp, cand, nil
	}
	return nil, Candidate{}, fmt.Errorf("core: every provider failed for reference %s: %w", req.ReferenceID, errors.Join(failures...))
}

func candidateName(candidate Candidate) string {
	if candidate.Name != "" {
		return candidate.Name
	}
	return candidate.Provider.Name()
}

func (e *Engine) buildTransaction(merchant *domain.Merchant, cmd CreatePaymentCommand, providerName string, resp *provider.CreatePaymentResponse, metadata map[string]string, description string) *domain.Transaction {
	now := e.now()
	tx := &domain.Transaction{
		ID:                e.newID(),
		MerchantID:        merchant.ID,
		Environment:       cmd.Environment,
		ReferenceID:       cmd.ReferenceID,
		ProviderName:      providerName,
		ProviderReference: resp.ProviderReference,
		Method:            cmd.Method,
		Channel:           cmd.Channel,
		Amount:            resp.Amount,
		Currency:          cmd.currency(),
		Status:            resp.Status,
		PaymentCode:       resp.PaymentCode,
		Actions:           resp.Actions,
		Customer:          cmd.Customer,
		Items:             cmd.Items,
		Description:       description,
		Metadata:          metadata,
		ExpiresAt:         resp.ExpiresAt,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	// Normalize anything the provider left unspecified.
	if tx.Amount <= 0 {
		tx.Amount = cmd.Amount
	}
	if !tx.Status.IsValid() {
		tx.Status = provider.StatusUnknown
	}
	if resp.ReferenceID != "" {
		tx.ReferenceID = resp.ReferenceID
	}
	if tx.Status == provider.StatusPaid && tx.PaidAt == nil {
		paid := now
		tx.PaidAt = &paid
	}
	return tx
}

// --- Inquiry ---------------------------------------------------------------

// Inquiry returns a transaction, optionally refreshing its status from the
// gateway.
func (e *Engine) Inquiry(ctx context.Context, q InquiryQuery) (*domain.Transaction, error) {
	tx, err := e.lookup(ctx, q.MerchantID, q.TransactionID, q.ReferenceID)
	if err != nil {
		return nil, err
	}
	if !q.Sync || tx.IsTerminal() || tx.ProviderReference == "" {
		return tx, nil
	}

	p, ok := e.registry.Get(tx.ProviderName)
	if !ok || !p.Capabilities().SupportsInquiry {
		return tx, nil
	}

	ctx = provider.WithTraceContext(ctx, provider.TraceContext{MerchantID: tx.MerchantID, Environment: tx.Environment, ReferenceID: tx.ReferenceID, TransactionID: tx.ID})
	resp, err := p.Inquiry(ctx, &provider.InquiryRequest{
		ReferenceID:       tx.ReferenceID,
		ProviderReference: tx.ProviderReference,
	})
	if err != nil {
		return nil, err
	}
	if err := e.applyStatus(ctx, tx, resp.Status, resp.PaidAt, ""); err != nil {
		return nil, err
	}
	return tx, nil
}

// --- Cancel ----------------------------------------------------------------

// Cancel cancels or expires a pending payment through its gateway.
func (e *Engine) Cancel(ctx context.Context, cmd CancelCommand) (*domain.Transaction, error) {
	tx, err := e.lookup(ctx, cmd.MerchantID, cmd.TransactionID, cmd.ReferenceID)
	if err != nil {
		return nil, err
	}
	if tx.IsTerminal() {
		return nil, fmt.Errorf("%w: transaction is already %s", domain.ErrUnprocessable, tx.Status)
	}

	p, ok := e.registry.Get(tx.ProviderName)
	if !ok {
		return nil, fmt.Errorf("%w: provider %q is not available", domain.ErrNoRoute, tx.ProviderName)
	}
	if !p.Capabilities().SupportsCancel {
		return nil, fmt.Errorf("%w: provider %q does not support cancel", provider.ErrUnsupportedOperation, tx.ProviderName)
	}

	reason := cmd.Reason
	if reason == "" {
		reason = "cancel"
	}
	ctx = provider.WithTraceContext(ctx, provider.TraceContext{MerchantID: tx.MerchantID, Environment: tx.Environment, ReferenceID: tx.ReferenceID, TransactionID: tx.ID})
	resp, err := p.Cancel(ctx, &provider.CancelRequest{
		ReferenceID:       tx.ReferenceID,
		ProviderReference: tx.ProviderReference,
		Reason:            reason,
	})
	if err != nil {
		return nil, err
	}

	next := resp.Status
	if !next.IsValid() {
		next = provider.StatusCancelled
	}
	if err := e.applyStatus(ctx, tx, next, nil, ""); err != nil {
		return nil, err
	}
	return tx, nil
}

// --- Inbound gateway events (webhooks) -------------------------------------

// HandleProviderEvent applies a normalized gateway event to the matching
// transaction, flags it, and emits an outbound notification to the merchant.
//
// It is idempotent: duplicate or out-of-order callbacks that would not change
// the transaction are accepted and ignored.
func (e *Engine) HandleProviderEvent(ctx context.Context, providerName, environment string, evt *provider.WebhookEvent) (*domain.Transaction, error) {
	if evt == nil || !evt.Correlatable() {
		return nil, domain.Invalid("webhook", "event carries no reference")
	}
	if !evt.Status.IsValid() {
		return nil, domain.Invalid("webhook", fmt.Sprintf("unknown status %q", evt.Status))
	}

	tx, err := e.correlate(ctx, providerName, environment, evt)
	if err != nil {
		return nil, err
	}

	if evt.Amount > 0 && tx.Amount > 0 && evt.Amount != tx.Amount {
		e.log.Warn("webhook amount mismatch",
			"transaction_id", tx.ID, "expected", tx.Amount, "reported", evt.Amount)
	}

	if !tx.CanTransitionTo(evt.Status) {
		e.log.Info("ignoring webhook: illegal or duplicate transition",
			"transaction_id", tx.ID, "from", tx.Status, "to", evt.Status)
		return tx, nil
	}
	if tx.Status == evt.Status {
		return tx, nil
	}
	if err := e.applyStatus(ctx, tx, evt.Status, evt.PaidAt, evt.EventType); err != nil {
		return nil, err
	}
	return tx, nil
}

func (e *Engine) correlate(ctx context.Context, providerName, environment string, evt *provider.WebhookEvent) (*domain.Transaction, error) {
	if evt.ProviderReference != "" {
		tx, err := e.txRepo.GetByProviderReference(ctx, providerName, evt.ProviderReference, environment)
		if err == nil {
			return tx, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	if evt.ReferenceID != "" {
		tx, err := e.txRepo.GetByProviderAndReference(ctx, providerName, evt.ReferenceID, environment)
		if err == nil {
			return tx, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: no transaction matches provider=%s reference=%s/%s",
		domain.ErrNotFound, providerName, evt.ReferenceID, evt.ProviderReference)
}

// --- internals -------------------------------------------------------------

// applyStatus transitions the transaction, persists it, and notifies the
// merchant. Unchanged/illegal transitions are no-ops.
func (e *Engine) applyStatus(ctx context.Context, tx *domain.Transaction, next provider.Status, paidAt *time.Time, failure string) error {
	if next == tx.Status {
		return nil
	}
	if !tx.ApplyStatus(next, paidAt, failure) {
		e.log.Info("ignoring status transition",
			"transaction_id", tx.ID, "from", tx.Status, "to", next)
		return nil
	}
	if err := e.txRepo.Update(ctx, tx); err != nil {
		return err
	}
	e.log.Info("transaction status changed",
		"transaction_id", tx.ID, "provider", tx.ProviderName, "status", tx.Status)
	e.notify(ctx, tx)
	return nil
}

func (e *Engine) notify(ctx context.Context, tx *domain.Transaction) {
	if e.notifier == nil {
		return
	}
	if err := e.notifier.Notify(ctx, tx); err != nil {
		// Notifications are queued and retried by the dispatcher; a failure
		// here only means the queue write failed.
		e.log.Error("enqueueing merchant notification failed",
			"transaction_id", tx.ID, "error", err.Error())
	}
}

func (e *Engine) loadMerchant(ctx context.Context, merchantID string) (*domain.Merchant, error) {
	if strings.TrimSpace(merchantID) == "" {
		return nil, domain.Invalid("merchant_id", "is required")
	}
	m, err := e.merchantRepo.GetByID(ctx, merchantID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: merchant %s", domain.ErrUnauthorized, merchantID)
		}
		return nil, err
	}
	return m, nil
}

func (e *Engine) lookup(ctx context.Context, merchantID, transactionID, referenceID string) (*domain.Transaction, error) {
	switch {
	case transactionID != "":
		tx, err := e.txRepo.GetByID(ctx, transactionID)
		if err != nil {
			return nil, err
		}
		if merchantID != "" && tx.MerchantID != merchantID {
			// Do not leak the existence of another merchant's transaction.
			return nil, domain.ErrNotFound
		}
		return tx, nil
	case referenceID != "":
		if merchantID == "" {
			return nil, domain.Invalid("merchant_id", "is required when looking up by reference_id")
		}
		return e.txRepo.GetByReference(ctx, merchantID, referenceID)
	default:
		return nil, domain.Invalid("id", "either transaction id or reference_id is required")
	}
}

func (e *Engine) candidates(merchant *domain.Merchant, forced string, req *RouteRequest) (*RouteDecision, error) {
	if forced != "" {
		candidates, err := e.router.OrderForProvider(forced, req)
		if err != nil {
			return nil, err
		}
		return &RouteDecision{Candidates: candidates}, nil
	}
	return e.router.Decide(merchant, req)
}
