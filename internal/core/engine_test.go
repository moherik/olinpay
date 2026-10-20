package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/internal/testutil/memory"
	"github.com/moherik/openpay.id/pkg/provider"
	"github.com/moherik/openpay.id/pkg/provider/mock"
)

// captureNotifier records every outbound notification the engine emits.
type captureNotifier struct {
	mu     sync.Mutex
	events []string
	txs    []*domain.Transaction
}

func (c *captureNotifier) Notify(_ context.Context, tx *domain.Transaction) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, tx.EventType())
	c.txs = append(c.txs, tx)
	return nil
}

func (c *captureNotifier) snapshot() ([]string, []*domain.Transaction) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...), append([]*domain.Transaction(nil), c.txs...)
}

// harness bundles everything a test needs to drive the engine.
type harness struct {
	engine   *core.Engine
	registry *provider.Registry
	store    *memory.Store
	notifier *captureNotifier
	gateway  *mock.Provider
	merchant *domain.Merchant
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	gateway, err := mock.New(provider.Config{"secret": "test-secret"})
	if err != nil {
		t.Fatalf("building mock provider: %v", err)
	}

	registry := provider.NewRegistry()
	registry.Add(gateway)

	store := memory.NewStore()
	merchant := &domain.Merchant{
		ID:         "AW9X2K4T7Z",
		Name:       "Test Merchant",
		APIKeyHash: "test-hash",
		Status:     domain.MerchantActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := store.Merchants().Create(context.Background(), merchant); err != nil {
		t.Fatalf("seeding merchant: %v", err)
	}

	notifier := &captureNotifier{}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	return &harness{
		engine: core.New(core.Deps{
			Registry:     registry,
			Router:       core.NewRouter(registry),
			Transactions: store.Transactions(),
			Merchants:    store.Merchants(),
			Notifier:     notifier,
			Logger:       quiet,
		}),
		registry: registry,
		store:    store,
		notifier: notifier,
		gateway:  gateway,
		merchant: merchant,
	}
}

func TestCreatePaymentPersistsAdapterNameAndEnvironment(t *testing.T) {
	h := newHarness(t)
	if _, err := h.registry.BuildAs("mock.sandbox", mock.Name, provider.Config{"secret": "test-secret"}); err != nil {
		t.Fatalf("building sandbox provider instance: %v", err)
	}
	h.merchant.Routing.Providers = []domain.RoutingProvider{{Name: "mock.sandbox", Enabled: true}}
	if err := h.store.Merchants().Update(context.Background(), h.merchant); err != nil {
		t.Fatalf("updating merchant route: %v", err)
	}

	cmd := h.createCommand()
	cmd.Environment = domain.EnvironmentSandbox
	tx, err := h.engine.CreatePayment(context.Background(), cmd)
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if tx.ProviderName != mock.Name {
		t.Fatalf("provider = %q, want %q", tx.ProviderName, mock.Name)
	}
	if tx.Environment != domain.EnvironmentSandbox {
		t.Fatalf("environment = %q, want sandbox", tx.Environment)
	}
}

func TestWebhookCorrelationUsesProviderAndEnvironment(t *testing.T) {
	h := newHarness(t)
	if _, err := h.registry.BuildAs("mock.sandbox", mock.Name, provider.Config{"secret": "test-secret"}); err != nil {
		t.Fatalf("building sandbox provider instance: %v", err)
	}
	h.merchant.Routing.Providers = []domain.RoutingProvider{{Name: "mock.sandbox", Enabled: true}}
	if err := h.store.Merchants().Update(context.Background(), h.merchant); err != nil {
		t.Fatalf("updating merchant route: %v", err)
	}
	cmd := h.createCommand()
	cmd.Environment = domain.EnvironmentSandbox
	tx, err := h.engine.CreatePayment(context.Background(), cmd)
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	if tx.ProviderName != mock.Name {
		t.Fatalf("provider = %q, want %q", tx.ProviderName, mock.Name)
	}
	evt := &provider.WebhookEvent{
		ReferenceID:       tx.ReferenceID,
		ProviderReference: tx.ProviderReference,
		Status:            provider.StatusPaid,
	}
	if _, err := h.engine.HandleProviderEvent(context.Background(), mock.Name, domain.EnvironmentProduction, evt); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("wrong-environment callback error = %v, want not found", err)
	}
	updated, err := h.engine.HandleProviderEvent(context.Background(), mock.Name, domain.EnvironmentSandbox, evt)
	if err != nil {
		t.Fatalf("HandleProviderEvent: %v", err)
	}
	if updated.ID != tx.ID || updated.Status != provider.StatusPaid {
		t.Fatalf("updated transaction = (%s, %s), want (%s, paid)", updated.ID, updated.Status, tx.ID)
	}
}

func (h *harness) createCommand() core.CreatePaymentCommand {
	return core.CreatePaymentCommand{
		MerchantID:  h.merchant.ID,
		ReferenceID: "order-1",
		Amount:      25000,
		Method:      provider.MethodVirtualAccount,
		Channel:     "MOCK_VA",
		Customer:    provider.Customer{Name: "Budi"},
		Description: "Test payment",
		Expiry:      time.Hour,
	}
}

func TestCreatePayment(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tx, err := h.engine.CreatePayment(ctx, h.createCommand())
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if tx.Status != provider.StatusPending {
		t.Errorf("status = %q, want %q", tx.Status, provider.StatusPending)
	}
	if tx.ProviderName != mock.Name {
		t.Errorf("provider = %q, want %q", tx.ProviderName, mock.Name)
	}
	if tx.ProviderReference == "" {
		t.Error("provider_reference must be set")
	}
	if tx.PaymentCode == "" {
		t.Error("virtual account payments should carry a payment code")
	}
}

func TestCreatePaymentIsIdempotentOnReference(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	cmd := h.createCommand()

	first, err := h.engine.CreatePayment(ctx, cmd)
	if err != nil {
		t.Fatalf("first CreatePayment: %v", err)
	}
	second, err := h.engine.CreatePayment(ctx, cmd)
	if err != nil {
		t.Fatalf("second CreatePayment: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("replay created a new transaction: %s != %s", first.ID, second.ID)
	}

	list, err := h.engine.List(ctx, repository.TransactionFilter{MerchantID: h.merchant.ID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected exactly 1 transaction, got %d", len(list))
	}
}

func TestCreatePaymentValidation(t *testing.T) {
	h := newHarness(t)
	cmd := h.createCommand()
	cmd.Amount = 0

	if _, err := h.engine.CreatePayment(context.Background(), cmd); !errors.Is(err, domain.ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
}

func TestWebhookMarksTransactionPaid(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tx, err := h.engine.CreatePayment(ctx, h.createCommand())
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	evt := h.parseCallback(t, map[string]any{
		"reference_id":       tx.ReferenceID,
		"provider_reference": tx.ProviderReference,
		"status":             "paid",
		"amount":             tx.Amount,
		"paid_at":            time.Now().Format(time.RFC3339),
	})

	updated, err := h.engine.HandleProviderEvent(ctx, mock.Name, tx.Environment, evt)
	if err != nil {
		t.Fatalf("HandleProviderEvent: %v", err)
	}
	if updated.Status != provider.StatusPaid {
		t.Fatalf("status = %q, want paid", updated.Status)
	}
	if updated.PaidAt == nil {
		t.Error("paid_at must be set")
	}

	events, _ := h.notifier.snapshot()
	if len(events) != 1 || events[0] != "payment.paid" {
		t.Errorf("notifications = %v, want [payment.paid]", events)
	}

	// A duplicate callback must be accepted and must not notify twice.
	if _, err := h.engine.HandleProviderEvent(ctx, mock.Name, tx.Environment, evt); err != nil {
		t.Fatalf("duplicate HandleProviderEvent: %v", err)
	}
	if events, _ := h.notifier.snapshot(); len(events) != 1 {
		t.Errorf("duplicate webhook emitted %d notifications, want 1", len(events))
	}
}

func TestWebhookForUnknownTransaction(t *testing.T) {
	h := newHarness(t)
	evt := &provider.WebhookEvent{
		ReferenceID:       "does-not-exist",
		ProviderReference: "MOCK-NOPE",
		Status:            provider.StatusPaid,
	}
	_, err := h.engine.HandleProviderEvent(context.Background(), mock.Name, domain.EnvironmentProduction, evt)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCancelPendingPayment(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tx, err := h.engine.CreatePayment(ctx, h.createCommand())
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	cancelled, err := h.engine.Cancel(ctx, core.CancelCommand{
		MerchantID:    h.merchant.ID,
		TransactionID: tx.ID,
	})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != provider.StatusCancelled {
		t.Errorf("status = %q, want cancelled", cancelled.Status)
	}

	// Cancelling a settled transaction is rejected.
	if _, err := h.engine.Cancel(ctx, core.CancelCommand{
		MerchantID:    h.merchant.ID,
		TransactionID: tx.ID,
	}); !errors.Is(err, domain.ErrUnprocessable) {
		t.Errorf("expected ErrUnprocessable, got %v", err)
	}
}

func TestInquirySync(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tx, err := h.engine.CreatePayment(ctx, h.createCommand())
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	// Simulate the customer paying, then ask the engine to reconcile.
	if err := h.gateway.Settle(tx.ReferenceID); err != nil {
		t.Fatalf("Settle: %v", err)
	}

	got, err := h.engine.Inquiry(ctx, core.InquiryQuery{
		MerchantID:    h.merchant.ID,
		TransactionID: tx.ID,
		Sync:          true,
	})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if got.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid after sync", got.Status)
	}
}

func (h *harness) parseCallback(t *testing.T, payload map[string]any) *provider.WebhookEvent {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshalling callback: %v", err)
	}
	req := &provider.WebhookRequest{Body: body}
	sig := mock.Sign("test-secret", body)
	req.Headers = map[string][]string{mock.SignatureHeader: {sig}}

	if err := h.gateway.VerifyWebhookSignature(req); err != nil {
		t.Fatalf("signature verification: %v", err)
	}
	evt, err := h.gateway.ParseWebhook(context.Background(), req)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	return evt
}
