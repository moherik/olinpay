package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moherik/openpay.id/internal/api"
	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/id"
	"github.com/moherik/openpay.id/internal/testutil/memory"
	"github.com/moherik/openpay.id/pkg/provider"
	"github.com/moherik/openpay.id/pkg/provider/mock"
)

const testAPIKey = "sk_test_abc123"

type fixture struct {
	server *httptest.Server
	store  *memory.Store
}

func setup(t *testing.T) *fixture {
	t.Helper()

	gateway, err := mock.New(provider.Config{})
	if err != nil {
		t.Fatalf("mock.New: %v", err)
	}
	registry := provider.NewRegistry()
	registry.Add(gateway)

	store := memory.NewStore()
	merchant := &domain.Merchant{
		ID:         "mch_api",
		Name:       "API Merchant",
		APIKeyHash: id.HashAPIKey(testAPIKey, ""),
		Status:     domain.MerchantActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := store.Merchants().Create(context.Background(), merchant); err != nil {
		t.Fatalf("seeding merchant: %v", err)
	}

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := core.New(core.Deps{
		Registry:     registry,
		Router:       core.NewRouter(registry),
		Transactions: store.Transactions(),
		Merchants:    store.Merchants(),
		Logger:       quiet,
	})

	srv := httptest.NewServer(api.NewServer(engine, store.Merchants(), "", quiet).Router())
	t.Cleanup(srv.Close)
	return &fixture{server: srv, store: store}
}

func (f *fixture) do(t *testing.T, method, path string, body any, withAuth bool) (*http.Response, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshalling body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withAuth {
		req.Header.Set("X-API-Key", testAPIKey)
	}

	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("performing request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

type paymentEnvelope struct {
	Data domain.Transaction `json:"data"`
}

func TestCreateAndFetchPayment(t *testing.T) {
	f := setup(t)

	payload := map[string]any{
		"reference_id": "INV-1001",
		"amount":       150000,
		"method":       "virtual_account",
		"channel":      "MOCK_VA",
		"customer":     map[string]string{"name": "Siti"},
	}

	resp, raw := f.do(t, http.MethodPost, "/v1/payments", payload, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", resp.StatusCode, raw)
	}
	var created paymentEnvelope
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}
	if created.Data.Status != provider.StatusPending {
		t.Errorf("status = %q, want pending", created.Data.Status)
	}
	if created.Data.ID == "" {
		t.Fatal("expected a transaction id")
	}

	resp, raw = f.do(t, http.MethodGet, "/v1/payments/"+created.Data.ID, nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", resp.StatusCode, raw)
	}
	var fetched paymentEnvelope
	if err := json.Unmarshal(raw, &fetched); err != nil {
		t.Fatalf("decoding get response: %v", err)
	}
	if fetched.Data.ID != created.Data.ID {
		t.Errorf("fetched id = %s, want %s", fetched.Data.ID, created.Data.ID)
	}
}

func TestCreatePaymentIsIdempotent(t *testing.T) {
	f := setup(t)
	payload := map[string]any{
		"reference_id": "INV-2002",
		"amount":       50000,
		"method":       "qris",
	}

	var first, second paymentEnvelope
	_, raw := f.do(t, http.MethodPost, "/v1/payments", payload, true)
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("decoding first response: %v", err)
	}
	_, raw = f.do(t, http.MethodPost, "/v1/payments", payload, true)
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatalf("decoding second response: %v", err)
	}
	if first.Data.ID != second.Data.ID {
		t.Errorf("idempotent replay returned a different transaction: %s != %s", first.Data.ID, second.Data.ID)
	}
}

func TestMissingAPIKeyIsUnauthorized(t *testing.T) {
	f := setup(t)
	resp, _ := f.do(t, http.MethodPost, "/v1/payments", map[string]any{"reference_id": "x"}, false)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestInvalidPayloadIsRejected(t *testing.T) {
	f := setup(t)
	resp, raw := f.do(t, http.MethodPost, "/v1/payments", map[string]any{
		"reference_id": "INV-3003",
		"amount":       0, // invalid
		"method":       "virtual_account",
	}, true)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", resp.StatusCode, raw)
	}
}

func TestCancelPayment(t *testing.T) {
	f := setup(t)
	var created paymentEnvelope
	_, raw := f.do(t, http.MethodPost, "/v1/payments", map[string]any{
		"reference_id": "INV-4004",
		"amount":       75000,
		"method":       "virtual_account",
	}, true)
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}

	resp, raw := f.do(t, http.MethodPost, "/v1/payments/"+created.Data.ID+"/cancel", nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, body = %s", resp.StatusCode, raw)
	}
	var cancelled paymentEnvelope
	if err := json.Unmarshal(raw, &cancelled); err != nil {
		t.Fatalf("decoding cancel response: %v", err)
	}
	if cancelled.Data.Status != provider.StatusCancelled {
		t.Errorf("status = %q, want cancelled", cancelled.Data.Status)
	}
}

func TestHealthzIsPublic(t *testing.T) {
	f := setup(t)
	resp, _ := f.do(t, http.MethodGet, "/healthz", nil, false)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSwaggerDashboardAndSpecArePublic(t *testing.T) {
	f := setup(t)

	resp, body := f.do(t, http.MethodGet, "/docs", nil, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("docs status = %d, body = %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Errorf("docs Content-Type = %q, want text/html", got)
	}
	if !strings.Contains(string(body), "SwaggerUIBundle") || !strings.Contains(string(body), "/openapi.yaml") {
		t.Errorf("docs page does not load Swagger UI and the embedded spec: %s", body)
	}

	resp, body = f.do(t, http.MethodGet, "/openapi.yaml", nil, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spec status = %d, body = %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "yaml") {
		t.Errorf("spec Content-Type = %q, want YAML", got)
	}
	if !strings.Contains(string(body), "openapi: 3.1.0") || !strings.Contains(string(body), "/v1/payments") {
		t.Errorf("unexpected OpenAPI spec response: %s", body)
	}
}
