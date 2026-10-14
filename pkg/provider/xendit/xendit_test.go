package xendit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

const testSecretKey = "xnd_development_testkey"

// testGateway stands in for the Xendit Payments API. It checks the Basic Auth
// credentials and the api-version header on every request (so a broken client is
// caught) and replays canned responses keyed by "METHOD /path".
type testGateway struct {
	t         *testing.T
	server    *httptest.Server
	mu        sync.Mutex
	responses map[string]string
	requests  map[string][]byte
	statuses  map[string]int
}

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	g := &testGateway{
		t:         t,
		responses: make(map[string]string),
		requests:  make(map[string][]byte),
		statuses:  make(map[string]int),
	}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

func (g *testGateway) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		g.t.Errorf("gateway: reading body: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testSecretKey+":"))
	if got := r.Header.Get(headerAuthorization); got != want {
		g.t.Errorf("gateway: bad Authorization on %s %s: got %q, want %q", r.Method, r.URL.Path, got, want)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if got := r.Header.Get(headerAPIVersion); got != defaultAPIVersion {
		g.t.Errorf("gateway: api-version = %q, want %q", got, defaultAPIVersion)
	}

	key := r.Method + " " + r.URL.Path
	g.mu.Lock()
	g.requests[key] = body
	response, ok := g.responses[key]
	status := g.statuses[key]
	g.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error_code":"DATA_NOT_FOUND","message":"not found"}`))
		return
	}
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(response))
}

// on registers a 200 response for "METHOD path".
func (g *testGateway) on(method, path, response string) {
	g.onStatus(method, path, http.StatusOK, response)
}

// onStatus registers a response with an explicit HTTP status.
func (g *testGateway) onStatus(method, path string, status int, response string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.responses[method+" "+path] = response
	g.statuses[method+" "+path] = status
}

func (g *testGateway) request(t *testing.T, method, path string) map[string]any {
	t.Helper()
	g.mu.Lock()
	raw, ok := g.requests[method+" "+path]
	g.mu.Unlock()
	if !ok {
		t.Fatalf("gateway did not receive %s %s", method, path)
	}
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("gateway request body is not JSON: %v", err)
	}
	return out
}

func newTestProvider(t *testing.T, g *testGateway, extra provider.Config) *Provider {
	t.Helper()
	cfg := provider.Config{
		"secret_key": testSecretKey,
		"base_url":   g.server.URL,
	}
	for k, v := range extra {
		cfg[k] = v
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// --- construction and capabilities -----------------------------------------

func TestNewRequiresSecretKey(t *testing.T) {
	if _, err := New(provider.Config{}); err == nil {
		t.Fatal("expected an error when secret_key is missing")
	}
}

func TestNewRejectsBadDurations(t *testing.T) {
	for _, key := range []string{"timeout", "default_expiry"} {
		if _, err := New(provider.Config{"secret_key": "k", key: "not-a-duration"}); err == nil {
			t.Errorf("expected an error for bad %s", key)
		}
	}
}

func TestCapabilities(t *testing.T) {
	p, err := New(provider.Config{"secret_key": "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	caps := p.Capabilities()
	for _, m := range []provider.Method{
		provider.MethodVirtualAccount, provider.MethodQRIS, provider.MethodEWallet,
		provider.MethodRetailOutlet, provider.MethodPaylater, provider.MethodCard,
	} {
		if !caps.SupportsMethod(m) {
			t.Errorf("capabilities do not advertise method %q", m)
		}
	}
	if caps.SupportsMethod(provider.MethodDirectDebit) {
		t.Error("direct debit should not be advertised")
	}
	if !caps.SupportsInquiry || !caps.SupportsCancel || !caps.SupportsWebhook {
		t.Errorf("capabilities = %+v", caps)
	}
	if !caps.SupportsChannel("bca") || !caps.SupportsChannel("QRIS") {
		t.Errorf("channels = %v", caps.Channels)
	}
}

func TestRegistryFactory(t *testing.T) {
	reg := provider.NewRegistry()
	p, err := reg.Build(Name, provider.Config{"secret_key": "k"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.Name() != Name {
		t.Errorf("name = %q, want %q", p.Name(), Name)
	}
}

// --- CreatePayment ---------------------------------------------------------

func TestCreateVirtualAccountBCA(t *testing.T) {
	g := newTestGateway(t)
	g.on(http.MethodPost, pathPaymentRequests, `{
		"business_id": "5f27a14a9bf05c73dd040bc8",
		"reference_id": "INV-0001",
		"payment_request_id": "pr-90392f42-d98a-49ef-a7f3-abcezas123",
		"type": "PAY",
		"country": "ID",
		"currency": "IDR",
		"request_amount": 25000,
		"channel_code": "BCA_VIRTUAL_ACCOUNT",
		"channel_properties": {
			"virtual_account_number": "8277081234567890",
			"expires_at": "2026-10-15T13:40:00Z"
		},
		"actions": [
			{"type": "PRESENT_TO_CUSTOMER", "value": "8277081234567890", "descriptor": "VIRTUAL_ACCOUNT_NUMBER"}
		],
		"status": "REQUIRES_ACTION",
		"created": "2026-10-14T13:40:00Z",
		"updated": "2026-10-14T13:40:00Z"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0001",
		Amount:      25000,
		Method:      provider.MethodVirtualAccount,
		Channel:     "bca",
		Customer:    provider.Customer{Name: "Budi Utomo"},
		Expiry:      time.Hour,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.ProviderReference != "pr-90392f42-d98a-49ef-a7f3-abcezas123" {
		t.Errorf("provider reference = %q", resp.ProviderReference)
	}
	if resp.ReferenceID != "INV-0001" {
		t.Errorf("reference id = %q", resp.ReferenceID)
	}
	if resp.PaymentCode != "8277081234567890" {
		t.Errorf("payment code = %q", resp.PaymentCode)
	}
	if resp.Status != provider.StatusPending {
		t.Errorf("status = %q, want pending", resp.Status)
	}
	if resp.Amount != 25000 {
		t.Errorf("amount = %d, want 25000", resp.Amount)
	}
	if resp.ExpiresAt == nil || resp.ExpiresAt.UTC().Format(time.RFC3339) != "2026-10-15T13:40:00Z" {
		t.Errorf("expires at = %v", resp.ExpiresAt)
	}

	sent := g.request(t, http.MethodPost, pathPaymentRequests)
	if sent["type"] != typePay {
		t.Errorf("type = %v, want PAY", sent["type"])
	}
	if sent["country"] != "ID" || sent["currency"] != "IDR" {
		t.Errorf("country/currency = %v/%v", sent["country"], sent["currency"])
	}
	if sent["request_amount"] != float64(25000) {
		t.Errorf("request_amount = %v, want 25000", sent["request_amount"])
	}
	if sent["channel_code"] != "BCA_VIRTUAL_ACCOUNT" {
		t.Errorf("channel_code = %v, want BCA_VIRTUAL_ACCOUNT", sent["channel_code"])
	}
	props, _ := sent["channel_properties"].(map[string]any)
	if props["display_name"] != "Budi Utomo" {
		t.Errorf("display_name = %v", props["display_name"])
	}
	if _, ok := props["expires_at"].(string); !ok {
		t.Errorf("expires_at = %v, want a timestamp string", props["expires_at"])
	}
}

func TestCreateQRIS(t *testing.T) {
	g := newTestGateway(t)
	g.on(http.MethodPost, pathPaymentRequests, `{
		"reference_id": "INV-QR",
		"payment_request_id": "pr-qr-123",
		"type": "PAY",
		"country": "ID",
		"currency": "IDR",
		"request_amount": 275000,
		"channel_code": "QRIS",
		"actions": [
			{"type": "PRESENT_TO_CUSTOMER", "value": "00020101021226610014ID.CO.QRIS.WWW", "descriptor": "QR_STRING"}
		],
		"status": "REQUIRES_ACTION"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-QR", Amount: 275000, Method: provider.MethodQRIS,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "00020101021226610014ID.CO.QRIS.WWW" {
		t.Errorf("payment code = %q", resp.PaymentCode)
	}
	found := false
	for _, a := range resp.Actions {
		if a.Type == provider.ActionQRCode {
			found = true
		}
	}
	if !found {
		t.Errorf("actions = %+v, want a qrcode action", resp.Actions)
	}
	sent := g.request(t, http.MethodPost, pathPaymentRequests)
	if sent["channel_code"] != channelQRIS {
		t.Errorf("channel_code = %v, want QRIS", sent["channel_code"])
	}
}

func TestCreateEWalletUsesReturnURL(t *testing.T) {
	g := newTestGateway(t)
	g.on(http.MethodPost, pathPaymentRequests, `{
		"reference_id": "INV-EW",
		"payment_request_id": "pr-ew-123",
		"type": "PAY",
		"country": "ID",
		"currency": "IDR",
		"request_amount": 50000,
		"channel_code": "DANA",
		"actions": [
			{"type": "REDIRECT_CUSTOMER", "value": "https://checkout.xendit.co/web/xyz", "descriptor": "WEB_URL"}
		],
		"status": "REQUIRES_ACTION"
	}`)

	p := newTestProvider(t, g, provider.Config{"return_url": "https://shop.example/done"})
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-EW", Amount: 50000, Method: provider.MethodEWallet, Channel: "dana",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	found := false
	for _, a := range resp.Actions {
		if a.Type == provider.ActionURL && a.Value == "https://checkout.xendit.co/web/xyz" {
			found = true
		}
	}
	if !found {
		t.Errorf("actions = %+v, want a url action", resp.Actions)
	}

	sent := g.request(t, http.MethodPost, pathPaymentRequests)
	if sent["channel_code"] != "DANA" {
		t.Errorf("channel_code = %v, want DANA", sent["channel_code"])
	}
	props, _ := sent["channel_properties"].(map[string]any)
	if props["success_return_url"] != "https://shop.example/done" {
		t.Errorf("success_return_url = %v", props["success_return_url"])
	}
}

func TestCreateRetailOutlet(t *testing.T) {
	g := newTestGateway(t)
	g.on(http.MethodPost, pathPaymentRequests, `{
		"reference_id": "INV-OTC",
		"payment_request_id": "pr-otc-123",
		"type": "REUSABLE_PAYMENT_CODE",
		"country": "ID",
		"currency": "IDR",
		"request_amount": 30000,
		"channel_code": "ALFAMART",
		"actions": [
			{"type": "PRESENT_TO_CUSTOMER", "value": "ALFAMART12345678", "descriptor": "PAYMENT_CODE"}
		],
		"status": "REQUIRES_ACTION"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-OTC", Amount: 30000, Method: provider.MethodRetailOutlet,
		Channel: "alfamart", Customer: provider.Customer{Name: "Siti"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "ALFAMART12345678" {
		t.Errorf("payment code = %q", resp.PaymentCode)
	}
	sent := g.request(t, http.MethodPost, pathPaymentRequests)
	if sent["type"] != typeReusablePaymentCode {
		t.Errorf("type = %v, want REUSABLE_PAYMENT_CODE", sent["type"])
	}
	props, _ := sent["channel_properties"].(map[string]any)
	if props["customer_name"] != "Siti" {
		t.Errorf("customer_name = %v", props["customer_name"])
	}
}

func TestCreateCardRequiresToken(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-CARD", Amount: 100000, Method: provider.MethodCard,
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCreateCardWithToken(t *testing.T) {
	g := newTestGateway(t)
	g.on(http.MethodPost, pathPaymentRequests, `{
		"reference_id": "INV-CARD",
		"payment_request_id": "pr-card-123",
		"type": "PAY",
		"country": "ID",
		"currency": "IDR",
		"request_amount": 100000,
		"payment_token_id": "pt-abc123",
		"status": "SUCCEEDED"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-CARD", Amount: 100000, Method: provider.MethodCard,
		Metadata: map[string]string{"payment_token_id": "pt-abc123"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid", resp.Status)
	}
	sent := g.request(t, http.MethodPost, pathPaymentRequests)
	if sent["payment_token_id"] != "pt-abc123" {
		t.Errorf("payment_token_id = %v", sent["payment_token_id"])
	}
	if _, ok := sent["channel_code"]; ok {
		t.Errorf("channel_code should be omitted for token payments, got %v", sent["channel_code"])
	}
	if meta, ok := sent["metadata"].(map[string]any); ok {
		if _, leaked := meta["payment_token_id"]; leaked {
			t.Errorf("control key leaked into metadata: %v", meta)
		}
	}
}

func TestCreatePaylater(t *testing.T) {
	g := newTestGateway(t)
	g.on(http.MethodPost, pathPaymentRequests, `{
		"reference_id": "INV-PL",
		"payment_request_id": "pr-pl-123",
		"type": "PAY",
		"country": "ID",
		"currency": "IDR",
		"request_amount": 60000,
		"channel_code": "AKULAKU",
		"actions": [
			{"type": "REDIRECT_CUSTOMER", "value": "https://checkout.xendit.co/web/pl", "descriptor": "WEB_URL"}
		],
		"status": "REQUIRES_ACTION"
	}`)

	p := newTestProvider(t, g, nil)
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-PL", Amount: 60000, Method: provider.MethodPaylater, Channel: "AKULAKU",
	}); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	sent := g.request(t, http.MethodPost, pathPaymentRequests)
	if sent["channel_code"] != "AKULAKU" {
		t.Errorf("channel_code = %v, want AKULAKU", sent["channel_code"])
	}
}

func TestCreateChannelPropertiesPassthrough(t *testing.T) {
	g := newTestGateway(t)
	g.on(http.MethodPost, pathPaymentRequests, `{
		"reference_id": "INV-OVO", "payment_request_id": "pr-ovo",
		"type": "PAY", "country": "ID", "currency": "IDR", "request_amount": 10000,
		"channel_code": "OVO", "status": "REQUIRES_ACTION"
	}`)

	p := newTestProvider(t, g, nil)
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-OVO", Amount: 10000, Method: provider.MethodEWallet, Channel: "OVO",
		Metadata: map[string]string{"channel_properties": `{"account_number":"081234567890"}`},
	}); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	sent := g.request(t, http.MethodPost, pathPaymentRequests)
	props, _ := sent["channel_properties"].(map[string]any)
	if props["account_number"] != "081234567890" {
		t.Errorf("channel_properties = %v, want account_number merged", props)
	}
}

func TestCreateRejectsBadChannelProperties(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-BAD", Amount: 10000, Method: provider.MethodQRIS,
		Metadata: map[string]string{"channel_properties": "not json"},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	cases := map[string]*provider.CreatePaymentRequest{
		"missing reference": {Amount: 1000, Method: provider.MethodQRIS},
		"zero amount":       {ReferenceID: "r", Amount: 0, Method: provider.MethodQRIS},
		"unknown method":    {ReferenceID: "r", Amount: 1000, Method: provider.Method("bogus")},
		"foreign currency":  {ReferenceID: "r", Amount: 1000, Currency: "USD", Method: provider.MethodQRIS},
		"bad channel":       {ReferenceID: "r", Amount: 1000, Method: provider.MethodVirtualAccount, Channel: "NOPE"},
	}
	for name, req := range cases {
		if _, err := p.CreatePayment(context.Background(), req); !errors.Is(err, provider.ErrInvalidRequest) {
			t.Errorf("%s: expected ErrInvalidRequest, got %v", name, err)
		}
	}
}

func TestCreateUnsupportedMethod(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-DD", Amount: 1000, Method: provider.MethodDirectDebit,
	})
	if !errors.Is(err, provider.ErrUnsupportedOperation) {
		t.Fatalf("expected ErrUnsupportedOperation, got %v", err)
	}
}

func TestCreateDuplicateReference(t *testing.T) {
	g := newTestGateway(t)
	g.onStatus(http.MethodPost, pathPaymentRequests, http.StatusConflict,
		`{"error_code":"DUPLICATE_REFERENCE","message":"reference_id already used"}`)

	p := newTestProvider(t, g, nil)
	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-DUP", Amount: 1000, Method: provider.MethodQRIS,
	})
	if !errors.Is(err, provider.ErrDuplicateReference) {
		t.Fatalf("expected ErrDuplicateReference, got %v", err)
	}
}

func TestCreateUnauthorized(t *testing.T) {
	g := newTestGateway(t)
	g.onStatus(http.MethodPost, pathPaymentRequests, http.StatusUnauthorized,
		`{"error_code":"INVALID_API_KEY","message":"invalid key"}`)

	p := newTestProvider(t, g, nil)
	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-401", Amount: 1000, Method: provider.MethodQRIS,
	})
	if !errors.Is(err, provider.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

// --- Inquiry ---------------------------------------------------------------

func TestInquiryPaid(t *testing.T) {
	g := newTestGateway(t)
	g.on(http.MethodGet, pathPaymentRequests+"/pr-90392f42", `{
		"reference_id": "INV-0001",
		"payment_request_id": "pr-90392f42",
		"type": "PAY",
		"country": "ID",
		"currency": "IDR",
		"request_amount": 25000,
		"channel_code": "BCA_VIRTUAL_ACCOUNT",
		"status": "SUCCEEDED",
		"latest_payment": {
			"payment_id": "py-1",
			"status": "SUCCEEDED",
			"captures": [
				{"capture_id": "cptr-1", "capture_timestamp": "2026-10-14T14:00:00Z", "capture_amount": 25000}
			]
		},
		"updated": "2026-10-14T14:00:00Z"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.Inquiry(context.Background(), &provider.InquiryRequest{
		ReferenceID: "INV-0001", ProviderReference: "pr-90392f42",
	})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if resp.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid", resp.Status)
	}
	if resp.Amount != 25000 {
		t.Errorf("amount = %d, want 25000", resp.Amount)
	}
	if resp.PaidAt == nil || resp.PaidAt.UTC().Format(time.RFC3339) != "2026-10-14T14:00:00Z" {
		t.Errorf("paid at = %v", resp.PaidAt)
	}
}

func TestInquiryRequiresProviderReference(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "INV-ONLY-REF"})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestInquiryNotFound(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ProviderReference: "pr-missing"})
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- Cancel ----------------------------------------------------------------

func TestCancel(t *testing.T) {
	g := newTestGateway(t)
	g.on(http.MethodPost, pathPaymentRequests+"/pr-90392f42/cancel", `{
		"reference_id": "INV-0001",
		"payment_request_id": "pr-90392f42",
		"type": "PAY",
		"country": "ID",
		"currency": "IDR",
		"request_amount": 25000,
		"status": "CANCELED"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{
		ReferenceID: "INV-0001", ProviderReference: "pr-90392f42", Reason: "cancel",
	})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusCancelled {
		t.Errorf("status = %q, want cancelled", resp.Status)
	}
}

func TestCancelRequiresProviderReference(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-ONLY-REF"})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

// --- webhook signature -----------------------------------------------------

func TestVerifyWebhookSignature(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, provider.Config{"callback_token": "secret-token"})

	ok := &provider.WebhookRequest{Headers: http.Header{headerCallbackToken: []string{"secret-token"}}}
	if err := p.VerifyWebhookSignature(ok); err != nil {
		t.Errorf("valid token rejected: %v", err)
	}

	bad := &provider.WebhookRequest{Headers: http.Header{headerCallbackToken: []string{"nope"}}}
	if err := p.VerifyWebhookSignature(bad); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("wrong token accepted: %v", err)
	}

	missing := &provider.WebhookRequest{Headers: http.Header{}}
	if err := p.VerifyWebhookSignature(missing); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("missing token accepted: %v", err)
	}
}

func TestVerifyWebhookSignatureUnconfigured(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil) // no callback_token

	req := &provider.WebhookRequest{Headers: http.Header{headerCallbackToken: []string{"anything"}}}
	if err := p.VerifyWebhookSignature(req); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature when unconfigured, got %v", err)
	}
}

// --- ParseWebhook ----------------------------------------------------------

func TestParseWebhookPaymentCapture(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	body := []byte(`{
		"event": "payment.capture",
		"business_id": "603f1c4172bbe840979fd408",
		"created": "2026-10-14T14:00:05Z",
		"data": {
			"payment_id": "py-66269236",
			"reference_id": "INV-0001",
			"payment_request_id": "pr-90392f42",
			"type": "PAY",
			"country": "ID",
			"currency": "IDR",
			"request_amount": 25000,
			"channel_code": "DANA",
			"status": "SUCCEEDED",
			"captures": [
				{"capture_id": "cptr-1", "capture_timestamp": "2026-10-14T14:00:00Z", "capture_amount": 25000}
			]
		}
	}`)

	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid", evt.Status)
	}
	if evt.ReferenceID != "INV-0001" {
		t.Errorf("reference = %q", evt.ReferenceID)
	}
	if evt.ProviderReference != "pr-90392f42" {
		t.Errorf("provider reference = %q", evt.ProviderReference)
	}
	if evt.Amount != 25000 {
		t.Errorf("amount = %d, want 25000", evt.Amount)
	}
	if evt.PaidAt == nil || evt.PaidAt.UTC().Format(time.RFC3339) != "2026-10-14T14:00:00Z" {
		t.Errorf("paid at = %v", evt.PaidAt)
	}
	if evt.EventType != "payment.capture" {
		t.Errorf("event type = %q", evt.EventType)
	}
}

func TestParseWebhookPaymentFailure(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: []byte(`{
		"event": "payment.failure",
		"data": {"reference_id": "INV-0001", "payment_request_id": "pr-1", "status": "FAILED", "request_amount": 25000}
	}`)})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.Status != provider.StatusFailed {
		t.Errorf("status = %q, want failed", evt.Status)
	}
}

func TestParseWebhookPaymentRequestExpiry(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: []byte(`{
		"event": "payment_request.expiry",
		"data": {"reference_id": "INV-0001", "payment_request_id": "pr-1", "status": "EXPIRED"}
	}`)})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.Status != provider.StatusExpired {
		t.Errorf("status = %q, want expired", evt.Status)
	}
}

func TestParseWebhookRefundSucceeded(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: []byte(`{
		"event": "refund.succeeded",
		"data": {"payment_id": "py-1", "reference_id": "INV-0001", "status": "SUCCEEDED", "request_amount": 25000}
	}`)})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.Status != provider.StatusRefunded {
		t.Errorf("status = %q, want refunded", evt.Status)
	}
	if evt.ProviderReference != "py-1" {
		t.Errorf("provider reference = %q, want the payment id fallback", evt.ProviderReference)
	}
}

func TestParseWebhookRejectsMalformed(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	cases := map[string][]byte{
		"empty":           {},
		"not json":        []byte(`{`),
		"not an envelope": []byte(`{"reference_id":"INV-1","status":"SUCCEEDED"}`),
		"unknown status":  []byte(`{"event":"payment.capture","data":{"reference_id":"r","status":"WEIRD"}}`),
	}
	for name, body := range cases {
		if _, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseWebhookNoReference(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{
		Body: []byte(`{"event":"payment.capture","data":{"status":"SUCCEEDED"}}`),
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

// --- mapping helpers -------------------------------------------------------

func TestParseAmountRejectsFraction(t *testing.T) {
	if _, err := parseAmount(json.Number("100.50")); err == nil {
		t.Fatal("expected sub-unit precision to be rejected")
	}
	got, err := parseAmount(json.Number("100.00"))
	if err != nil || got != 100 {
		t.Fatalf("parseAmount(100.00) = %d, %v", got, err)
	}
}

func TestMapStatus(t *testing.T) {
	cases := map[string]provider.Status{
		"SUCCEEDED":          provider.StatusPaid,
		"REQUIRES_ACTION":    provider.StatusPending,
		"ACCEPTING_PAYMENTS": provider.StatusPending,
		"AUTHORIZED":         provider.StatusPending,
		"FAILED":             provider.StatusFailed,
		"CANCELED":           provider.StatusCancelled,
		"EXPIRED":            provider.StatusExpired,
		"":                   provider.StatusUnknown,
	}
	for in, want := range cases {
		if got := mapStatus(in); got != want {
			t.Errorf("mapStatus(%q) = %q, want %q", in, got, want)
		}
	}
}
