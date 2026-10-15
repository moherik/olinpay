package midtrans

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

const testServerKey = "SB-Mid-server-TESTKEY"

// testGateway stands in for Midtrans' legacy Core API. It enforces the Basic
// Auth header on every request and serves canned responses keyed by
// "METHOD /path".
type testGateway struct {
	t          *testing.T
	server     *httptest.Server
	mu         sync.Mutex
	responses  map[string]string
	statuses   map[string]int
	lastBody   map[string][]byte
	lastHeader map[string]http.Header
}

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	g := &testGateway{
		t:          t,
		responses:  make(map[string]string),
		statuses:   make(map[string]int),
		lastBody:   make(map[string][]byte),
		lastHeader: make(map[string]http.Header),
	}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

// respond registers a 200 response for method+path.
func (g *testGateway) respond(method, path, body string) {
	g.respondStatus(method, path, http.StatusOK, body)
}

// respondStatus registers a response with an explicit HTTP status.
func (g *testGateway) respondStatus(method, path string, status int, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.responses[method+" "+path] = body
	g.statuses[method+" "+path] = status
}

func (g *testGateway) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path

	g.mu.Lock()
	g.lastBody[key] = body
	g.lastHeader[key] = r.Header.Clone()
	resp, ok := g.responses[key]
	status := g.statuses[key]
	g.mu.Unlock()

	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(testServerKey+":"))
	if r.Header.Get(headerAuthorization) != wantAuth {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status_code":"401","status_message":"unauthorized"}`))
		return
	}
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status_code":"404","status_message":"not found"}`))
		return
	}
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(resp))
}

func (g *testGateway) header(t *testing.T, method, path, name string) string {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	h, ok := g.lastHeader[method+" "+path]
	if !ok {
		t.Fatalf("gateway did not receive %s %s", method, path)
	}
	return h.Get(name)
}

func (g *testGateway) body(t *testing.T, method, path string) map[string]any {
	t.Helper()
	g.mu.Lock()
	raw, ok := g.lastBody[method+" "+path]
	g.mu.Unlock()
	if !ok {
		t.Fatalf("gateway did not receive %s %s", method, path)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("gateway body is not JSON: %v", err)
	}
	return out
}

func newTestProvider(t *testing.T, g *testGateway, extra provider.Config) *Provider {
	t.Helper()
	cfg := provider.Config{
		"server_key": testServerKey,
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

// --- config -----------------------------------------------------------------

func TestNewRequiresConfig(t *testing.T) {
	if _, err := New(provider.Config{}); err == nil {
		t.Error("New without server_key should fail")
	}
	if _, err := New(provider.Config{"server_key": "s", "timeout": "nope"}); err == nil {
		t.Error("expected an error for an invalid timeout")
	}
	if _, err := New(provider.Config{"server_key": "s", "default_expiry": "nope"}); err == nil {
		t.Error("expected an error for an invalid default_expiry")
	}

	p, err := New(provider.Config{"server_key": "s"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.baseURL != defaultBaseURL {
		t.Errorf("base url = %q, want %q", p.baseURL, defaultBaseURL)
	}
	if p.qrisAcquirer != qrisDefaultAcquirer {
		t.Errorf("qris acquirer = %q, want %q", p.qrisAcquirer, qrisDefaultAcquirer)
	}
}

func TestRegistryFactory(t *testing.T) {
	cfg := provider.Config{"server_key": "s"}
	built, err := provider.Default().Build(Name, cfg)
	if err != nil {
		t.Fatalf("Build(%q): %v", Name, err)
	}
	if built.Name() != Name {
		t.Errorf("name = %q, want %q", built.Name(), Name)
	}
	if Name != "midtrans" {
		t.Errorf("legacy Core API Name = %q, want %q", Name, "midtrans")
	}
	if _, err := provider.NewRegistry().Build(Name, cfg); err != nil {
		t.Fatalf("Build on a fresh registry: %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	caps := p.Capabilities()

	for _, m := range []provider.Method{
		provider.MethodVirtualAccount, provider.MethodQRIS,
		provider.MethodEWallet, provider.MethodRetailOutlet,
	} {
		if !caps.SupportsMethod(m) {
			t.Errorf("expected %q to be supported", m)
		}
	}
	for _, m := range []provider.Method{provider.MethodCard, provider.MethodPaylater, provider.MethodDirectDebit} {
		if caps.SupportsMethod(m) {
			t.Errorf("Core API adapter does not support %q, but it was advertised", m)
		}
	}
	for _, ch := range []string{"BCA", "MANDIRI", "QRIS", "GOPAY", "INDOMARET", "alfamart"} {
		if !caps.SupportsChannel(ch) {
			t.Errorf("expected channel %q to be advertised", ch)
		}
	}
	if caps.SupportsChannel("NOPE") {
		t.Error("unknown channel should be rejected")
	}
	if !caps.SupportsInquiry || !caps.SupportsCancel || !caps.SupportsWebhook || !caps.SupportsSandbox {
		t.Error("expected inquiry/cancel/webhook/sandbox support")
	}
}

// --- CreatePayment ----------------------------------------------------------

func TestCreateBankTransferVA(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathCharge, `{
		"status_code":"201","status_message":"OK",
		"transaction_id":"trx-1","order_id":"INV-0001","gross_amount":"25000.00",
		"payment_type":"bank_transfer","transaction_status":"pending",
		"va_numbers":[{"bank":"bca","va_number":"12345678"}],
		"expiry_time":"2026-10-16 10:00:00"
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0001",
		Amount:      25000,
		Currency:    provider.IDR,
		Method:      provider.MethodVirtualAccount,
		Channel:     "bca",
		Customer:    provider.Customer{Name: "Budi Utomo", Email: "budi@example.com"},
		Items:       []provider.Item{{Name: "Widget", Price: 25000, Quantity: 1}},
		Expiry:      time.Hour,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "12345678" {
		t.Errorf("PaymentCode = %q", resp.PaymentCode)
	}
	if resp.ProviderReference != "trx-1" || resp.ReferenceID != "INV-0001" {
		t.Errorf("references = %q/%q", resp.ProviderReference, resp.ReferenceID)
	}
	if resp.Status != provider.StatusPending || resp.Amount != 25000 {
		t.Errorf("status/amount = %q/%d", resp.Status, resp.Amount)
	}
	if resp.ExpiresAt == nil {
		t.Error("expected an expiry")
	}
	if len(resp.Actions) != 1 || resp.Actions[0].Type != provider.ActionDisplay {
		t.Errorf("Actions = %+v, want one display action", resp.Actions)
	}

	body := g.body(t, http.MethodPost, pathCharge)
	if body["payment_type"] != "bank_transfer" {
		t.Errorf("payment_type = %v", body["payment_type"])
	}
	bt, _ := body["bank_transfer"].(map[string]any)
	if bt["bank"] != "bca" {
		t.Errorf("bank_transfer.bank = %v", bt["bank"])
	}
	td, _ := body["transaction_details"].(map[string]any)
	if td["order_id"] != "INV-0001" || td["gross_amount"] != float64(25000) {
		t.Errorf("transaction_details = %v", td)
	}
	cd, _ := body["customer_details"].(map[string]any)
	if cd["first_name"] != "Budi" || cd["last_name"] != "Utomo" || cd["email"] != "budi@example.com" {
		t.Errorf("customer_details = %v", cd)
	}
	if _, ok := body["custom_expiry"]; !ok {
		t.Error("expected custom_expiry")
	}
	if got := g.header(t, http.MethodPost, pathCharge, headerAuthorization); !strings.HasPrefix(got, "Basic ") {
		t.Errorf("Authorization = %q", got)
	}
}

func TestCreatePermataVA(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathCharge, `{
		"status_code":"201","status_message":"OK",
		"transaction_id":"trx-1","order_id":"INV-1","gross_amount":"10000.00",
		"payment_type":"bank_transfer","transaction_status":"pending",
		"permata_va_number":"8562938472983"
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 10000, Method: provider.MethodVirtualAccount, Channel: "PERMATA",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "8562938472983" {
		t.Errorf("PaymentCode = %q", resp.PaymentCode)
	}
}

func TestCreateMandiriECHannel(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathCharge, `{
		"status_code":"201","status_message":"OK",
		"transaction_id":"trx-1","order_id":"INV-2","gross_amount":"44000.00",
		"payment_type":"echannel","transaction_status":"pending",
		"biller_code":"70012","bill_key":"68100249875"
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-2", Amount: 44000, Method: provider.MethodVirtualAccount, Channel: "MANDIRI",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "7001268100249875" {
		t.Errorf("PaymentCode = %q", resp.PaymentCode)
	}
	if len(resp.Actions) != 1 || resp.Actions[0].Type != provider.ActionDisplay {
		t.Errorf("Actions = %+v", resp.Actions)
	}

	body := g.body(t, http.MethodPost, pathCharge)
	if body["payment_type"] != "echannel" {
		t.Errorf("payment_type = %v", body["payment_type"])
	}
	ec, _ := body["echannel"].(map[string]any)
	if ec["bill_info1"] != "Payment" {
		t.Errorf("echannel.bill_info1 = %v", ec["bill_info1"])
	}
}

func TestCreateQRIS(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathCharge, `{
		"status_code":"201","status_message":"OK",
		"transaction_id":"trx-1","order_id":"INV-3","gross_amount":"275000.00",
		"payment_type":"qris","transaction_status":"pending","acquirer":"gopay",
		"qr_string":"00020101021226610014COM.GO-JEK.WWW",
		"actions":[{"name":"generate-qr-code","method":"GET","url":"https://api.midtrans.com/v2/qris/INV-3/qr-code"}]
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-3", Amount: 275000, Method: provider.MethodQRIS, Channel: "QRIS",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "00020101021226610014COM.GO-JEK.WWW" {
		t.Errorf("PaymentCode = %q", resp.PaymentCode)
	}
	if len(resp.Actions) != 2 || resp.Actions[0].Type != provider.ActionQRCode || resp.Actions[1].Type != provider.ActionURL {
		t.Errorf("Actions = %+v, want a QR payload and a URL", resp.Actions)
	}

	body := g.body(t, http.MethodPost, pathCharge)
	if body["payment_type"] != "qris" {
		t.Errorf("payment_type = %v", body["payment_type"])
	}
	q, _ := body["qris"].(map[string]any)
	if q["acquirer"] != "gopay" {
		t.Errorf("qris.acquirer = %v", q["acquirer"])
	}
}

func TestCreateEWallet(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathCharge, `{
		"status_code":"201","status_message":"OK",
		"transaction_id":"trx-1","order_id":"INV-4","gross_amount":"10000.00",
		"payment_type":"gopay","transaction_status":"pending",
		"actions":[{"name":"deeplink-redirect","method":"GET","url":"https://gojek.link/pay/abc"}]
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-4", Amount: 10000, Method: provider.MethodEWallet, Channel: "GOPAY",
		Metadata: map[string]string{"return_url": "https://shop.example/return"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if len(resp.Actions) != 1 || resp.Actions[0].Type != provider.ActionDeeplink {
		t.Fatalf("Actions = %+v, want a deeplink", resp.Actions)
	}

	body := g.body(t, http.MethodPost, pathCharge)
	if body["payment_type"] != "gopay" {
		t.Errorf("payment_type = %v", body["payment_type"])
	}
	gp, _ := body["gopay"].(map[string]any)
	if gp["enable_callback"] != true || gp["callback_url"] != "https://shop.example/return" {
		t.Errorf("gopay = %v", gp)
	}
}

func TestCreateShopeePay(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathCharge, `{
		"status_code":"201","status_message":"OK",
		"transaction_id":"trx-1","order_id":"INV-5","gross_amount":"10000.00",
		"payment_type":"shopeepay","transaction_status":"pending",
		"actions":[{"name":"deeplink-redirect","method":"GET","url":"https://shopee.co.id/pay"}]
	}`)
	p := newTestProvider(t, g, provider.Config{"shopee_pay_callback_url": "https://shop.example/sp"})

	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-5", Amount: 10000, Method: provider.MethodEWallet, Channel: "SHOPEEPAY",
	}); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	body := g.body(t, http.MethodPost, pathCharge)
	sp, _ := body["shopeepay"].(map[string]any)
	if sp["callback_url"] != "https://shop.example/sp" {
		t.Errorf("shopeepay.callback_url = %v", sp["callback_url"])
	}
}

func TestCreateRetailOutlet(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathCharge, `{
		"status_code":"201","status_message":"OK",
		"transaction_id":"trx-1","order_id":"INV-6","gross_amount":"15000.00",
		"payment_type":"cstore","transaction_status":"pending",
		"store":"indomaret","payment_code":"12345678"
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-6", Amount: 15000, Method: provider.MethodRetailOutlet, Channel: "INDOMARET",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "12345678" {
		t.Errorf("PaymentCode = %q", resp.PaymentCode)
	}
	if len(resp.Actions) != 1 || resp.Actions[0].Type != provider.ActionDisplay {
		t.Errorf("Actions = %+v", resp.Actions)
	}

	body := g.body(t, http.MethodPost, pathCharge)
	cs, _ := body["cstore"].(map[string]any)
	if cs["store"] != "indomaret" {
		t.Errorf("cstore.store = %v", cs["store"])
	}
}

func TestCreateErrors(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodVirtualAccount,
	}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest for a missing VA channel, got %v", err)
	}
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodVirtualAccount, Channel: "XYZ",
	}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest for an unknown VA channel, got %v", err)
	}
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodEWallet,
	}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest for a missing wallet channel, got %v", err)
	}
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodRetailOutlet, Channel: "NOPE",
	}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest for an unknown retail channel, got %v", err)
	}
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodQRIS, Currency: "USD",
	}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest for a non-IDR amount, got %v", err)
	}
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		Amount: 1000, Method: provider.MethodQRIS,
	}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest for a missing reference, got %v", err)
	}
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodCard,
	}); !errors.Is(err, provider.ErrUnsupportedOperation) {
		t.Errorf("expected ErrUnsupportedOperation for a card, got %v", err)
	}
}

func TestCreateSurfacesGatewayError(t *testing.T) {
	g := newTestGateway(t)
	// Midtrans reports a duplicate order_id with HTTP 200 and status_code 406.
	g.respond("POST", pathCharge, `{"status_code":"406","status_message":"Duplicate order id"}`)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodQRIS,
	})
	if !errors.Is(err, provider.ErrDuplicateReference) {
		t.Fatalf("expected ErrDuplicateReference, got %v", err)
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Code != "406" {
		t.Errorf("expected the gateway code to be preserved, got %#v", err)
	}
}

func TestCreateSurfacesHTTPError(t *testing.T) {
	g := newTestGateway(t)
	g.respondStatus("POST", pathCharge, http.StatusInternalServerError, `{"status_code":"500","status_message":"boom"}`)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodQRIS,
	})
	if !errors.Is(err, provider.ErrGateway) {
		t.Fatalf("expected ErrGateway, got %v", err)
	}
}

// --- Inquiry ----------------------------------------------------------------

func TestInquiry(t *testing.T) {
	g := newTestGateway(t)
	g.respond("GET", "/v2/INV-1/status", `{
		"status_code":"200","status_message":"OK",
		"transaction_id":"trx-1","order_id":"INV-1","gross_amount":"10000.00",
		"payment_type":"bank_transfer","transaction_status":"settlement",
		"settlement_time":"2026-10-14 10:00:00",
		"va_numbers":[{"bank":"bca","va_number":"12345678"}]
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "INV-1"})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if resp.Status != provider.StatusPaid || resp.Amount != 10000 {
		t.Errorf("status/amount = %q/%d", resp.Status, resp.Amount)
	}
	if resp.ProviderReference != "trx-1" || resp.PaymentCode != "12345678" {
		t.Errorf("providerRef/code = %q/%q", resp.ProviderReference, resp.PaymentCode)
	}
	if resp.PaidAt == nil {
		t.Error("expected paid_at")
	}
}

// TestInquiryByProviderReference verifies that a transaction_id returned as the
// provider reference can address the status endpoint when no reference_id is
// supplied.
func TestInquiryByProviderReference(t *testing.T) {
	g := newTestGateway(t)
	g.respond("GET", "/v2/trx-9/status", `{
		"status_code":"200","status_message":"OK",
		"transaction_id":"trx-9","order_id":"INV-9","gross_amount":"5000.00",
		"transaction_status":"pending"
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ProviderReference: "trx-9"})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if resp.Status != provider.StatusPending {
		t.Errorf("Status = %q, want pending", resp.Status)
	}
}

func TestInquiryNotFound(t *testing.T) {
	g := newTestGateway(t)
	g.respondStatus("GET", "/v2/NOPE/status", http.StatusNotFound, `{"status_code":"404","status_message":"Transaction doesn't exist"}`)
	p := newTestProvider(t, g, nil)

	_, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "NOPE"})
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := p.Inquiry(context.Background(), &provider.InquiryRequest{}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for an empty request, got %v", err)
	}
}

// --- Cancel -----------------------------------------------------------------

func TestCancel(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", "/v2/INV-1/cancel", `{
		"status_code":"200","status_message":"OK",
		"transaction_id":"trx-1","order_id":"INV-1","transaction_status":"cancel"
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-1"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusCancelled {
		t.Errorf("Status = %q, want cancelled", resp.Status)
	}
}

func TestCancelExpire(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", "/v2/INV-2/expire", `{
		"status_code":"200","status_message":"OK",
		"transaction_id":"trx-2","order_id":"INV-2","transaction_status":"expire"
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-2", Reason: "expire"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusExpired {
		t.Errorf("Status = %q, want expired", resp.Status)
	}
}

func TestCancelErrors(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if _, err := p.Cancel(context.Background(), &provider.CancelRequest{}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for an empty request, got %v", err)
	}
}

// --- ParseWebhook -----------------------------------------------------------

func TestParseWebhook(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	body := []byte(`{
		"transaction_time":"2026-10-14 10:00:00","transaction_status":"settlement",
		"transaction_id":"trx-1","status_message":"midtrans payment notification",
		"status_code":"200","signature_key":"abc","payment_type":"bank_transfer",
		"order_id":"INV-1","gross_amount":"10000.00","fraud_status":"accept",
		"settlement_time":"2026-10-14 10:05:00",
		"va_numbers":[{"bank":"bca","va_number":"12345678"}]
	}`)
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventVAPayment {
		t.Errorf("EventType = %q", evt.EventType)
	}
	if evt.ReferenceID != "INV-1" || evt.ProviderReference != "trx-1" {
		t.Errorf("references = %q/%q", evt.ReferenceID, evt.ProviderReference)
	}
	if evt.Status != provider.StatusPaid || evt.Amount != 10000 {
		t.Errorf("status/amount = %q/%d", evt.Status, evt.Amount)
	}
	if evt.PaidAt == nil {
		t.Error("expected paid_at")
	}
}

func TestParseWebhookEventTypes(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	cases := map[string]string{
		"bank_transfer": EventVAPayment,
		"echannel":      EventVAPayment,
		"qris":          EventQRISPayment,
		"gopay":         EventEWalletPayment,
		"shopeepay":     EventEWalletPayment,
		"cstore":        EventRetailPayment,
	}
	for paymentType, want := range cases {
		body := []byte(`{"order_id":"INV-1","transaction_id":"trx-1","transaction_status":"pending","gross_amount":"1000.00","payment_type":"` + paymentType + `"}`)
		evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
		if err != nil {
			t.Fatalf("ParseWebhook(%s): %v", paymentType, err)
		}
		if evt.EventType != want {
			t.Errorf("payment_type %s => %q, want %q", paymentType, evt.EventType, want)
		}
	}
}

func TestParseWebhookStatuses(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	cases := []struct {
		status string
		want   provider.Status
	}{
		{"pending", provider.StatusPending},
		{"settlement", provider.StatusPaid},
		{"cancel", provider.StatusCancelled},
		{"expire", provider.StatusExpired},
		{"deny", provider.StatusFailed},
	}
	for _, tc := range cases {
		body := []byte(`{"order_id":"INV-1","transaction_id":"trx-1","transaction_status":"` + tc.status + `","gross_amount":"1000.00"}`)
		evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
		if err != nil {
			t.Fatalf("ParseWebhook(%s): %v", tc.status, err)
		}
		if evt.Status != tc.want {
			t.Errorf("status %s => %q, want %q", tc.status, evt.Status, tc.want)
		}
	}
}

func TestParseWebhookNotCorrelatable(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if _, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: []byte(`{"transaction_status":"pending"}`)}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for an uncorrelatable body, got %v", err)
	}
	if _, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for an empty body, got %v", err)
	}
}

// --- VerifyWebhookSignature -------------------------------------------------

func TestVerifyWebhookSignature(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	body := []byte(`{"order_id":"INV-1","status_code":"200","gross_amount":"10000.00","transaction_id":"trx-1"}`)
	sign := func(b []byte) string {
		var n notification
		if err := json.Unmarshal(b, &n); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return notificationSignature(n.OrderID, n.StatusCode, n.GrossAmount, testServerKey)
	}

	valid, _ := json.Marshal(map[string]any{
		"order_id": "INV-1", "status_code": "200", "gross_amount": "10000.00",
		"transaction_id": "trx-1", "signature_key": sign(body),
	})
	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: valid}); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}

	tampered, _ := json.Marshal(map[string]any{
		"order_id": "INV-1", "status_code": "200", "gross_amount": "99999.00",
		"signature_key": sign(body),
	})
	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: tampered}); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for a tampered body, got %v", err)
	}

	missing, _ := json.Marshal(map[string]any{"order_id": "INV-1", "status_code": "200", "gross_amount": "10000.00"})
	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: missing}); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature without signature_key, got %v", err)
	}
	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: []byte("not json")}); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for a malformed body, got %v", err)
	}
	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{}); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for an empty body, got %v", err)
	}
}

// --- mapping and helpers ----------------------------------------------------

func TestMapTransactionStatus(t *testing.T) {
	cases := []struct {
		status string
		fraud  string
		want   provider.Status
	}{
		{"capture", "accept", provider.StatusPaid},
		{"capture", "deny", provider.StatusFailed},
		{"settlement", "", provider.StatusPaid},
		{"pending", "", provider.StatusPending},
		{"authorize", "", provider.StatusPending},
		{"deny", "", provider.StatusFailed},
		{"failure", "", provider.StatusFailed},
		{"cancel", "", provider.StatusCancelled},
		{"expire", "", provider.StatusExpired},
		{"refund", "", provider.StatusRefunded},
		{"chargeback", "", provider.StatusRefunded},
		{"nonsense", "", provider.StatusUnknown},
		{"", "", provider.StatusUnknown},
	}
	for _, tc := range cases {
		if got := mapTransactionStatus(tc.status, tc.fraud); got != tc.want {
			t.Errorf("mapTransactionStatus(%q,%q) = %q, want %q", tc.status, tc.fraud, got, tc.want)
		}
	}
}

func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		httpStatus int
		code       string
		want       error
	}{
		{200, "200", nil},
		{0, "406", provider.ErrDuplicateReference},
		{409, "", provider.ErrDuplicateReference},
		{0, "401", provider.ErrUnauthorized},
		{403, "", provider.ErrUnauthorized},
		{0, "404", provider.ErrNotFound},
		{404, "", provider.ErrNotFound},
		{0, "400", provider.ErrInvalidRequest},
		{422, "", provider.ErrInvalidRequest},
		{500, "", provider.ErrGateway},
	}
	for _, tc := range cases {
		if got := classifyStatus(tc.httpStatus, tc.code); got != tc.want {
			t.Errorf("classifyStatus(%d,%q) = %v, want %v", tc.httpStatus, tc.code, got, tc.want)
		}
	}
}

func TestParseAmount(t *testing.T) {
	cases := []struct {
		value   string
		want    provider.Amount
		wantErr bool
	}{
		{"25000.00", 25000, false},
		{"10000", 10000, false},
		{"1", 1, false},
		{"", 0, false},
		{"1000.50", 0, true},
		{"abc", 0, true},
	}
	for _, tc := range cases {
		got, err := parseAmount(tc.value)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseAmount(%q): expected an error", tc.value)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseAmount(%q) = %d, %v; want %d", tc.value, got, err, tc.want)
		}
	}
}

func TestParseTime(t *testing.T) {
	tm := parseTime("2026-10-14 10:00:00")
	if tm == nil {
		t.Fatal("expected a parsed time")
	}
	if _, offset := tm.Zone(); offset != 7*60*60 {
		t.Errorf("offset = %d, want +7h", offset)
	}
	if iso := parseTime("2026-10-14T10:00:00+07:00"); iso == nil {
		t.Error("expected an ISO-8601 timestamp to parse")
	}
	if parseTime("") != nil || parseTime("not-a-time") != nil {
		t.Error("empty or unparseable values must yield nil")
	}
}

func TestCustomExpiryOf(t *testing.T) {
	if customExpiryOf(0) != nil {
		t.Error("zero duration must yield nil")
	}
	day := customExpiryOf(24 * time.Hour)
	if day == nil || day.Unit != "day" || day.ExpiryDuration != 1 {
		t.Errorf("customExpiryOf(24h) = %+v", day)
	}
	hour := customExpiryOf(2 * time.Hour)
	if hour == nil || hour.Unit != "hour" || hour.ExpiryDuration != 2 {
		t.Errorf("customExpiryOf(2h) = %+v", hour)
	}
	minute := customExpiryOf(90 * time.Minute)
	if minute == nil || minute.Unit != "minute" || minute.ExpiryDuration != 90 {
		t.Errorf("customExpiryOf(90m) = %+v", minute)
	}
}

func TestAddressID(t *testing.T) {
	cases := []struct {
		reference, providerRef, want string
	}{
		{"INV-1", "trx-1", "INV-1"},
		{"", "trx-1", "trx-1"},
		{"INV-1", "", "INV-1"},
		{"", "", ""},
	}
	for _, tc := range cases {
		if got := addressID(tc.reference, tc.providerRef); got != tc.want {
			t.Errorf("addressID(%q,%q) = %q, want %q", tc.reference, tc.providerRef, got, tc.want)
		}
	}
}

func TestSplitName(t *testing.T) {
	cases := map[string][2]string{
		"":               {"", ""},
		"Budi":           {"Budi", ""},
		"Budi Utomo":     {"Budi", "Utomo"},
		"Budi Santoso W": {"Budi", "Santoso W"},
	}
	for in, want := range cases {
		first, last := splitName(in)
		if first != want[0] || last != want[1] {
			t.Errorf("splitName(%q) = (%q,%q), want (%q,%q)", in, first, last, want[0], want[1])
		}
	}
}

func TestNotificationSignature(t *testing.T) {
	// Deterministic cross-check against a hand-computed SHA-512 of the
	// concatenation.
	got := notificationSignature("INV-1", "200", "10000.00", "SB-Mid-server-TESTKEY")
	want := "0868c1f6133e6b8a938fe40e069c989e7f9b00444387ec22db2ef1afe73b4541970bc65372407fdd3ef0f1966fefab95bd4e9812e49a3e00c84d275570a5313a"
	if got != want {
		t.Errorf("notificationSignature = %q, want %q", got, want)
	}
	// The signature must be sensitive to every input.
	base := notificationSignature("INV-1", "200", "10000.00", "k")
	for _, other := range []string{
		notificationSignature("INV-2", "200", "10000.00", "k"),
		notificationSignature("INV-1", "201", "10000.00", "k"),
		notificationSignature("INV-1", "200", "10000.01", "k"),
		notificationSignature("INV-1", "200", "10000.00", "k2"),
	} {
		if other == base {
			t.Errorf("signature did not change for a modified input")
		}
	}
}
