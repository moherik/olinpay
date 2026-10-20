package webhook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/moherik/openpay.id/internal/config"
	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/testutil/memory"
	"github.com/moherik/openpay.id/internal/webhook"
	"github.com/moherik/openpay.id/pkg/provider"
	"github.com/moherik/openpay.id/pkg/provider/mock"
)

const gatewaySecret = "hook-secret"

type fixture struct {
	server *httptest.Server
	engine *core.Engine
	store  *memory.Store
}

func setup(t *testing.T) *fixture {
	t.Helper()

	gateway, err := mock.New(provider.Config{"secret": gatewaySecret})
	if err != nil {
		t.Fatalf("mock.New: %v", err)
	}
	return setupWithProvider(t, gateway)
}

// setupWithProvider builds the receiver around an arbitrary provider so tests
// can exercise optional interfaces.
func setupWithProvider(t *testing.T, p provider.Provider) *fixture {
	t.Helper()

	registry := provider.NewRegistry()
	registry.Add(p)

	store := memory.NewStore()
	merchant := &domain.Merchant{
		ID:            "AW9X2K4T7Z",
		Name:          "Webhook Merchant",
		CallbackURL:   "http://merchant.test/hooks/openpay",
		WebhookSecret: "whsec_test",
		Status:        domain.MerchantActive,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	if err := store.Merchants().Create(context.Background(), merchant); err != nil {
		t.Fatalf("seeding merchant: %v", err)
	}

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	dispatcher := webhook.NewDispatcher(store.Webhooks(), store.Merchants(), config.WebhookConfig{}, quiet)
	engine := core.New(core.Deps{
		Registry:     registry,
		Router:       core.NewRouter(registry),
		Transactions: store.Transactions(),
		Merchants:    store.Merchants(),
		Notifier:     dispatcher,
		Logger:       quiet,
	})

	receiver := webhook.NewReceiver(registry, engine, store.Inbound(), quiet)
	router := chi.NewRouter()
	receiver.Mount(router)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &fixture{server: srv, engine: engine, store: store}
}

func createPayment(t *testing.T, f *fixture) *domain.Transaction {
	t.Helper()
	tx, err := f.engine.CreatePayment(context.Background(), core.CreatePaymentCommand{
		MerchantID:  "AW9X2K4T7Z",
		ReferenceID: "INV-WH-1",
		Amount:      99000,
		Method:      provider.MethodVirtualAccount,
		Channel:     "MOCK_VA",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	return tx
}

func post(t *testing.T, f *fixture, path string, body []byte, signature string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set(mock.SignatureHeader, signature)
	}
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("performing request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func TestReceiverAppliesCallbackAndQueuesMerchantWebhook(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	tx := createPayment(t, f)

	body, _ := json.Marshal(map[string]any{
		"reference_id":       tx.ReferenceID,
		"provider_reference": tx.ProviderReference,
		"status":             "paid",
		"amount":             tx.Amount,
		"paid_at":            time.Now().Format(time.RFC3339),
	})

	resp, raw := post(t, f, "/webhooks/mock", body, mock.Sign(gatewaySecret, body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("receiver status = %d, body = %s", resp.StatusCode, raw)
	}

	got, err := f.engine.Inquiry(ctx, core.InquiryQuery{MerchantID: "AW9X2K4T7Z", TransactionID: tx.ID})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if got.Status != provider.StatusPaid {
		t.Fatalf("status = %q, want paid", got.Status)
	}

	due, err := f.store.Webhooks().Due(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("queued deliveries = %d, want 1", len(due))
	}
	if due[0].EventType != "payment.paid" {
		t.Errorf("event = %q, want payment.paid", due[0].EventType)
	}
}

func TestReceiverAcceptsProviderCallbackPathSuffix(t *testing.T) {
	f := setup(t)
	tx := createPayment(t, f)
	body, _ := json.Marshal(map[string]any{
		"reference_id":       tx.ReferenceID,
		"provider_reference": tx.ProviderReference,
		"status":             "paid",
		"amount":             tx.Amount,
	})

	resp, raw := post(t, f, "/webhooks/mock/v1.0/transfer-va/payment", body, mock.Sign(gatewaySecret, body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("receiver status = %d, body = %s", resp.StatusCode, raw)
	}
}

func TestReceiverRejectsBadSignature(t *testing.T) {
	f := setup(t)
	tx := createPayment(t, f)

	body, _ := json.Marshal(map[string]any{
		"reference_id": tx.ReferenceID,
		"status":       "paid",
	})

	resp, _ := post(t, f, "/webhooks/mock", body, "not-a-valid-signature")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestReceiverUnknownProvider(t *testing.T) {
	f := setup(t)
	resp, _ := post(t, f, "/webhooks/nope", []byte(`{}`), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ackProvider wraps the mock and also implements provider.WebhookResponder, so
// the receiver must reply with its own body instead of the generic envelope.
type ackProvider struct {
	*mock.Provider
	status int
	body   []byte
}

func (a *ackProvider) WebhookResponse(*provider.WebhookEvent) (int, []byte) {
	return a.status, a.body
}

func TestReceiverHonoursProviderAcknowledgement(t *testing.T) {
	gateway, err := mock.New(provider.Config{"secret": gatewaySecret})
	if err != nil {
		t.Fatalf("mock.New: %v", err)
	}
	ack := &ackProvider{
		Provider: gateway,
		status:   http.StatusOK,
		body:     []byte(`{"responseCode":"2002500","responseMessage":"Successful"}`),
	}
	f := setupWithProvider(t, ack)

	tx := createPayment(t, f)
	body, _ := json.Marshal(map[string]any{
		"reference_id": tx.ReferenceID,
		"status":       "paid",
		"amount":       tx.Amount,
	})

	resp, raw := post(t, f, "/webhooks/mock", body, mock.Sign(gatewaySecret, body))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	if string(raw) != string(ack.body) {
		t.Errorf("ack body = %s, want %s", raw, ack.body)
	}
}
