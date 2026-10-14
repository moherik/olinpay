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

const testServerKey = "SB-Mid-server-test"

// testGateway stands in for the Midtrans Core API. It checks the Basic Auth
// credentials on every request (so a broken client is caught) and replays
// canned responses keyed by path.
type testGateway struct {
	t         *testing.T
	server    *httptest.Server
	mu        sync.Mutex
	responses map[string]string
	requests  map[string][]byte
}

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	g := &testGateway{
		t:         t,
		responses: make(map[string]string),
		requests:  make(map[string][]byte),
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

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testServerKey+":"))
	if got := r.Header.Get(headerAuthorization); got != want {
		g.t.Errorf("gateway: bad Authorization on %s %s: got %q, want %q", r.Method, r.URL.Path, got, want)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	key := r.Method + " " + r.URL.Path
	g.mu.Lock()
	if len(body) > 0 {
		g.requests[key] = body
	} else {
		g.requests[key] = []byte{}
	}
	response, ok := g.responses[r.URL.Path]
	g.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status_code":"404","status_message":"Transaction doesn't exist."}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(response))
}

func (g *testGateway) on(path, response string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.responses[path] = response
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

func (g *testGateway) called(t *testing.T, method, path string) bool {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.requests[method+" "+path]
	return ok
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

// --- CreatePayment ---------------------------------------------------------

func TestCreateVirtualAccountBCA(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathCharge, `{
		"status_code": "201",
		"status_message": "Success, Bank Transfer transaction is created",
		"transaction_id": "be03df7d-2f97-4c8c-a53c-8959f1b67295",
		"order_id": "INV-0001",
		"merchant_id": "G812785002",
		"gross_amount": "25000.00",
		"currency": "IDR",
		"payment_type": "bank_transfer",
		"transaction_time": "2019-10-23 16:33:49",
		"transaction_status": "pending",
		"va_numbers": [{"bank": "bca", "va_number": "812785002530231"}],
		"fraud_status": "accept",
		"expiry_time": "2019-10-24 16:33:49"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0001",
		Amount:      25000,
		Method:      provider.MethodVirtualAccount,
		Channel:     "bca",
		Customer:    provider.Customer{Name: "Budi Utomo", Email: "budi@example.com"},
		Expiry:      time.Hour,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.ProviderReference != "be03df7d-2f97-4c8c-a53c-8959f1b67295" {
		t.Errorf("provider reference = %q", resp.ProviderReference)
	}
	if resp.PaymentCode != "812785002530231" {
		t.Errorf("payment code = %q", resp.PaymentCode)
	}
	if resp.Status != provider.StatusPending {
		t.Errorf("status = %q, want pending", resp.Status)
	}
	if resp.Amount != 25000 {
		t.Errorf("amount = %d, want 25000", resp.Amount)
	}
	if resp.ExpiresAt == nil {
		t.Error("expected an expiry")
	}

	sent := g.request(t, http.MethodPost, pathCharge)
	if sent["payment_type"] != "bank_transfer" {
		t.Errorf("payment_type = %v", sent["payment_type"])
	}
	trx, _ := sent["transaction_details"].(map[string]any)
	if trx["order_id"] != "INV-0001" {
		t.Errorf("order_id = %v", trx["order_id"])
	}
	if trx["gross_amount"] != float64(25000) {
		t.Errorf("gross_amount = %v, want 25000", trx["gross_amount"])
	}
	bt, _ := sent["bank_transfer"].(map[string]any)
	if bt["bank"] != "bca" {
		t.Errorf("bank = %v, want bca (lower-cased)", bt["bank"])
	}
	ce, _ := sent["custom_expiry"].(map[string]any)
	if ce == nil || ce["unit"] != "minute" || ce["expiry_duration"] != float64(60) {
		t.Errorf("custom_expiry = %v, want 60 minutes", ce)
	}
}

func TestCreateVirtualAccountMandiriUsesEChannel(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathCharge, `{
		"status_code": "201",
		"status_message": "OK, Mandiri Bill transaction is successful",
		"transaction_id": "abb2d93f-dae3-4183-936d-4145423ad72f",
		"order_id": "INV-0002",
		"gross_amount": "44000.00",
		"currency": "IDR",
		"payment_type": "echannel",
		"transaction_status": "pending",
		"fraud_status": "accept",
		"bill_key": "778347787706",
		"biller_code": "70012"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0002", Amount: 44000, Method: provider.MethodVirtualAccount, Channel: "MANDIRI",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "778347787706" {
		t.Errorf("payment code = %q, want the bill key", resp.PaymentCode)
	}
	found := false
	for _, a := range resp.Actions {
		if a.Type == provider.ActionDisplay && strings.Contains(a.Value, "70012") && strings.Contains(a.Value, "778347787706") {
			found = true
		}
	}
	if !found {
		t.Errorf("actions = %+v, want a display action with biller code and bill key", resp.Actions)
	}

	sent := g.request(t, http.MethodPost, pathCharge)
	if sent["payment_type"] != "echannel" {
		t.Errorf("payment_type = %v, want echannel", sent["payment_type"])
	}
}

func TestCreateVirtualAccountRequiresChannel(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0003", Amount: 10000, Method: provider.MethodVirtualAccount,
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCreateQRIS(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathCharge, `{
		"status_code": "201",
		"status_message": "QRIS transaction is created",
		"transaction_id": "0d8178e1-c6c7-4ab4-81a6-893be9d924ab",
		"order_id": "order03",
		"gross_amount": "275000.00",
		"currency": "IDR",
		"payment_type": "qris",
		"transaction_status": "pending",
		"fraud_status": "accept",
		"acquirer": "gopay",
		"actions": [
			{"name": "generate-qr-code", "method": "GET", "url": "https://api.midtrans.com/v2/qris/0d8178e1/qr-code"},
			{"name": "generate-qr-code-v2", "method": "GET", "url": "https://api.midtrans.com/v4/qris/A120/qr-code"}
		]
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "order03", Amount: 275000, Method: provider.MethodQRIS,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "https://api.midtrans.com/v2/qris/0d8178e1/qr-code" {
		t.Errorf("payment code = %q, want the QR image url", resp.PaymentCode)
	}
	if len(resp.Actions) != 1 {
		t.Fatalf("actions = %+v, want a single QR action (v2 deduplicated)", resp.Actions)
	}
	if resp.Actions[0].Type != provider.ActionURL {
		t.Errorf("action type = %q, want url", resp.Actions[0].Type)
	}

	sent := g.request(t, http.MethodPost, pathCharge)
	if sent["payment_type"] != "qris" {
		t.Errorf("payment_type = %v", sent["payment_type"])
	}
	// QRIS does not accept custom_expiry.
	if _, ok := sent["custom_expiry"]; ok {
		t.Error("custom_expiry must not be sent for QRIS")
	}
}

func TestCreateEWalletGoPay(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathCharge, `{
		"status_code": "201",
		"status_message": "GO-PAY billing created",
		"transaction_id": "e48447d1-cfa9-4b02-b163-2e915d4417ac",
		"order_id": "SAMPLE-ORDER-ID-01",
		"gross_amount": "10000.00",
		"currency": "IDR",
		"payment_type": "gopay",
		"transaction_status": "pending",
		"actions": [
			{"name": "deeplink-redirect", "method": "GET", "url": "gojek://gopay/merchanttransfer?tref=abc"},
			{"name": "generate-qr-code", "method": "GET", "url": "https://api.midtrans.com/v2/gopay/e48447d1/qr-code"}
		]
	}`)

	p := newTestProvider(t, g, provider.Config{"callback_url": "https://openpay.example/webhooks/midtrans"})
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "SAMPLE-ORDER-ID-01",
		Amount:      10000,
		Method:      provider.MethodEWallet,
		Channel:     "gopay",
		Metadata:    map[string]string{"return_url": "myapp://checkout/finish"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if len(resp.Actions) != 2 {
		t.Fatalf("actions = %+v, want a deeplink and a QR url", resp.Actions)
	}
	if resp.Actions[0].Type != provider.ActionDeeplink || resp.Actions[1].Type != provider.ActionURL {
		t.Errorf("action types = %q, %q", resp.Actions[0].Type, resp.Actions[1].Type)
	}

	sent := g.request(t, http.MethodPost, pathCharge)
	if sent["payment_type"] != "gopay" {
		t.Errorf("payment_type = %v", sent["payment_type"])
	}
	gopay, _ := sent["gopay"].(map[string]any)
	if gopay["enable_callback"] != true || gopay["callback_url"] != "myapp://checkout/finish" {
		t.Errorf("gopay = %v, want the return_url as callback", gopay)
	}
}

func TestCreateRetailOutletAlfamart(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathCharge, `{
		"status_code": "201",
		"status_message": "Success, cstore transaction is successful",
		"transaction_id": "d615df87-c96f-4f5c-9d35-2d740d54c1a9",
		"order_id": "order-101",
		"gross_amount": "44000.00",
		"payment_type": "cstore",
		"transaction_status": "pending",
		"fraud_status": "accept",
		"payment_code": "8127740588870520",
		"store": "alfamart"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "order-101", Amount: 44000, Method: provider.MethodRetailOutlet, Channel: "alfamart",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "8127740588870520" {
		t.Errorf("payment code = %q", resp.PaymentCode)
	}

	sent := g.request(t, http.MethodPost, pathCharge)
	cstore, _ := sent["cstore"].(map[string]any)
	if cstore["store"] != "alfamart" {
		t.Errorf("store = %v, want alfamart", cstore)
	}
}

func TestCreateCardRequiresToken(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-9", Amount: 10000, Method: provider.MethodCard, Channel: "CARD",
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}

	g.on(pathCharge, `{
		"status_code": "200", "status_message": "Success, Credit Card transaction is successful",
		"transaction_id": "1eae238a-cb9e-4f92-b284-aac8b39e4eab", "order_id": "INV-9",
		"gross_amount": "10000.00", "payment_type": "credit_card",
		"transaction_status": "capture", "fraud_status": "accept"
	}`)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-9", Amount: 10000, Method: provider.MethodCard, Channel: "CARD",
		Metadata: map[string]string{"card_token": "token-abc"},
	})
	if err != nil {
		t.Fatalf("CreatePayment with token: %v", err)
	}
	if resp.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid for a captured card", resp.Status)
	}
}

func TestCreateUnsupportedMethod(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 10000, Method: provider.MethodDirectDebit,
	})
	if !errors.Is(err, provider.ErrUnsupportedOperation) {
		t.Fatalf("expected ErrUnsupportedOperation, got %v", err)
	}
}

func TestCreateRejectsNonIDR(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 10000, Method: provider.MethodQRIS, Currency: "USD",
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCreateSurfacesDuplicateReference(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathCharge, `{"status_code":"406","status_message":"order_id has been used"}`)

	p := newTestProvider(t, g, nil)
	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0001", Amount: 25000, Method: provider.MethodVirtualAccount, Channel: "BCA",
	})
	if !errors.Is(err, provider.ErrDuplicateReference) {
		t.Fatalf("expected ErrDuplicateReference, got %v", err)
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Code != "406" {
		t.Errorf("expected the gateway code to be preserved, got %#v", err)
	}
}

// --- Inquiry ---------------------------------------------------------------

func TestInquirySettlement(t *testing.T) {
	g := newTestGateway(t)
	g.on("/v2/INV-0001/status", `{
		"status_code": "200",
		"status_message": "Success, transaction is found",
		"transaction_id": "57d5293c-e65f-4a29-95e4-5959c3fa335b",
		"order_id": "INV-0001",
		"gross_amount": "25000.00",
		"payment_type": "bank_transfer",
		"transaction_status": "settlement",
		"fraud_status": "accept",
		"settlement_time": "2021-06-15 18:45:28",
		"expiry_time": "2021-06-16 18:45:28"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "INV-0001"})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if resp.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid", resp.Status)
	}
	if resp.Amount != 25000 {
		t.Errorf("amount = %d, want 25000", resp.Amount)
	}
	if resp.PaidAt == nil {
		t.Error("expected a paid_at for a settled payment")
	}
	if !g.called(t, http.MethodGet, "/v2/INV-0001/status") {
		t.Error("expected a GET status call addressed by order_id")
	}
}

func TestInquiryNotFound(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "nope"})
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- Cancel ----------------------------------------------------------------

func TestCancel(t *testing.T) {
	g := newTestGateway(t)
	g.on("/v2/INV-0001/cancel", `{
		"status_code": "200", "status_message": "Success, transaction is canceled",
		"transaction_id": "tx-1", "order_id": "INV-0001", "gross_amount": "25000.00",
		"payment_type": "bank_transfer", "transaction_status": "cancel"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-0001", Reason: "cancel"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusCancelled {
		t.Errorf("status = %q, want cancelled", resp.Status)
	}
	if !g.called(t, http.MethodPost, "/v2/INV-0001/cancel") {
		t.Error("expected a POST cancel call")
	}
}

func TestCancelExpire(t *testing.T) {
	g := newTestGateway(t)
	g.on("/v2/INV-0001/expire", `{
		"status_code": "200", "status_message": "Success, transaction is expired",
		"transaction_id": "tx-1", "order_id": "INV-0001", "gross_amount": "25000.00",
		"payment_type": "bank_transfer", "transaction_status": "expire"
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-0001", Reason: "expire"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusExpired {
		t.Errorf("status = %q, want expired", resp.Status)
	}
	if !g.called(t, http.MethodPost, "/v2/INV-0001/expire") {
		t.Error("expected a POST expire call")
	}
}

// --- ParseWebhook ----------------------------------------------------------

func TestParseWebhookSettlement(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	body := []byte(`{
		"transaction_time": "2021-06-15 18:45:13",
		"transaction_status": "settlement",
		"transaction_id": "513f1f01-c9da-474c-9fc9-d5c64364b709",
		"status_message": "midtrans payment notification",
		"status_code": "200",
		"signature_key": "deadbeef",
		"settlement_time": "2021-06-15 18:45:28",
		"payment_type": "bank_transfer",
		"order_id": "Order-5100",
		"merchant_id": "G141532850",
		"gross_amount": "154600.00",
		"fraud_status": "accept",
		"currency": "IDR"
	}`)

	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid", evt.Status)
	}
	if evt.ReferenceID != "Order-5100" {
		t.Errorf("reference = %q", evt.ReferenceID)
	}
	if evt.ProviderReference != "513f1f01-c9da-474c-9fc9-d5c64364b709" {
		t.Errorf("provider reference = %q", evt.ProviderReference)
	}
	if evt.Amount != 154600 {
		t.Errorf("amount = %d, want 154600", evt.Amount)
	}
	if evt.PaidAt == nil {
		t.Error("expected paid_at from settlement_time")
	}
	if evt.EventType != "bank_transfer.settlement" {
		t.Errorf("event type = %q", evt.EventType)
	}
}

func TestParseWebhookExpire(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	body := []byte(`{"order_id":"order03","transaction_id":"tx","transaction_status":"expire",
		"gross_amount":"275000.00","payment_type":"qris","status_code":"202"}`)
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.Status != provider.StatusExpired {
		t.Errorf("status = %q, want expired", evt.Status)
	}
	if evt.PaidAt != nil {
		t.Error("an expired payment must not carry paid_at")
	}
}

func TestParseWebhookCardChallengeIsPending(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	body := []byte(`{"order_id":"C17550","transaction_id":"tx","transaction_status":"capture",
		"fraud_status":"challenge","gross_amount":"10000.00","payment_type":"credit_card","status_code":"201"}`)
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.Status != provider.StatusPending {
		t.Errorf("status = %q, want pending for a challenged capture", evt.Status)
	}
}

func TestParseWebhookUnrecognizedStatus(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	body := []byte(`{"order_id":"x","transaction_id":"tx","transaction_status":"weird","gross_amount":"1.00"}`)
	_, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

// --- VerifyWebhookSignature ------------------------------------------------

func TestVerifyWebhookSignature(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	orderID, statusCode, gross := "INV-0001", "200", "25000.00"
	makeBody := func(sig string) []byte {
		b, _ := json.Marshal(map[string]string{
			"order_id": orderID, "status_code": statusCode, "gross_amount": gross,
			"signature_key": sig, "transaction_id": "tx", "transaction_status": "settlement",
		})
		return b
	}

	valid := makeBody(signatureKey(testServerKey, orderID, statusCode, gross))
	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: valid}); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}

	// Uppercase hex must still verify.
	upper := makeBody(strings.ToUpper(signatureKey(testServerKey, orderID, statusCode, gross)))
	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: upper}); err != nil {
		t.Errorf("uppercase signature rejected: %v", err)
	}

	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: makeBody("deadbeef")}); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for a bad signature, got %v", err)
	}

	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: []byte(`{"order_id":"x"}`)}); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for a missing signature, got %v", err)
	}

	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: []byte(`not json`)}); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for malformed JSON, got %v", err)
	}
}

// --- mapping and config ----------------------------------------------------

func TestMapTransactionStatus(t *testing.T) {
	cases := []struct {
		status, fraud string
		want          provider.Status
	}{
		{"capture", "accept", provider.StatusPaid},
		{"capture", "challenge", provider.StatusPending},
		{"settlement", "", provider.StatusPaid},
		{"pending", "", provider.StatusPending},
		{"authorize", "", provider.StatusPending},
		{"deny", "", provider.StatusFailed},
		{"cancel", "", provider.StatusCancelled},
		{"expire", "", provider.StatusExpired},
		{"refund", "", provider.StatusRefunded},
		{"partial_refund", "", provider.StatusRefunded},
		{"chargeback", "", provider.StatusFailed},
		{"", "", provider.StatusUnknown},
		{"nonsense", "", provider.StatusUnknown},
	}
	for _, tc := range cases {
		if got := mapTransactionStatus(tc.status, tc.fraud); got != tc.want {
			t.Errorf("mapTransactionStatus(%q,%q) = %q, want %q", tc.status, tc.fraud, got, tc.want)
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
		if err != nil {
			t.Errorf("parseAmount(%q): %v", tc.value, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseAmount(%q) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

func TestParseTime(t *testing.T) {
	tm := parseTime("2021-06-15 18:45:28")
	if tm == nil {
		t.Fatal("expected a parsed time")
	}
	// Midtrans timestamps are WIB (UTC+7).
	if _, offset := tm.Zone(); offset != 7*60*60 {
		t.Errorf("offset = %d, want +7h", offset)
	}
	if parseTime("") != nil {
		t.Error("empty value must yield nil")
	}
	if parseTime("not-a-time") != nil {
		t.Error("unparseable value must yield nil")
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

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(provider.Config{}); err == nil {
		t.Error("expected an error when server_key is missing")
	}
	if _, err := New(provider.Config{"server_key": "k", "timeout": "nope"}); err == nil {
		t.Error("expected an error for an invalid timeout")
	}
	if _, err := New(provider.Config{"server_key": "k", "default_expiry": "nope"}); err == nil {
		t.Error("expected an error for an invalid default_expiry")
	}
	p, err := New(provider.Config{"server_key": "k"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.baseURL != defaultBaseURL {
		t.Errorf("base url = %q, want %q", p.baseURL, defaultBaseURL)
	}
}

func TestRegistryFactory(t *testing.T) {
	cfg := provider.Config{"server_key": "SB-Mid-server-x"}

	built, err := provider.Default().Build(Name, cfg)
	if err != nil {
		t.Fatalf("Build(%q): %v", Name, err)
	}
	if built.Name() != Name {
		t.Errorf("name = %q, want %q", built.Name(), Name)
	}

	reg := provider.NewRegistry()
	if _, err := reg.Build(Name, cfg); err != nil {
		t.Fatalf("Build on a fresh registry: %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	caps := p.Capabilities()
	for _, method := range []provider.Method{
		provider.MethodVirtualAccount, provider.MethodQRIS, provider.MethodEWallet,
		provider.MethodRetailOutlet, provider.MethodPaylater, provider.MethodCard,
	} {
		if !caps.SupportsMethod(method) {
			t.Errorf("expected %q to be supported", method)
		}
	}
	if caps.SupportsMethod(provider.MethodDirectDebit) {
		t.Error("direct debit is not supported")
	}
	if !caps.SupportsChannel("BCA") || !caps.SupportsChannel("GOPAY") || !caps.SupportsChannel("QRIS") {
		t.Error("expected VA, e-wallet and QRIS channels to be advertised")
	}
	// Channel matching must tolerate the case/whitespace callers use.
	if !caps.SupportsChannel("bca") || !caps.SupportsChannel("  mandiri  ") {
		t.Error("expected channel matching to be case-insensitive")
	}
	if caps.SupportsChannel("NOPE") {
		t.Error("expected an unknown channel to be rejected")
	}
	if !caps.SupportsInquiry || !caps.SupportsCancel || !caps.SupportsWebhook {
		t.Error("expected inquiry, cancel and webhook support")
	}
}

func TestExpiryDefaultAndOverride(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if got := p.expiry(0); got != defaultExpiry {
		t.Errorf("expiry(0) = %v, want %v", got, defaultExpiry)
	}
	if got := p.expiry(2 * time.Hour); got != 2*time.Hour {
		t.Errorf("expiry(2h) = %v, want 2h", got)
	}

	ce := customExpiryFrom(90 * time.Minute)
	if ce == nil || ce.ExpiryDuration != 90 || ce.Unit != "minute" {
		t.Errorf("customExpiryFrom(90m) = %+v", ce)
	}
	if customExpiryFrom(0) != nil {
		t.Error("a zero duration must yield nil")
	}
	// Sub-minute durations are rounded up to one minute.
	if ce := customExpiryFrom(10 * time.Second); ce == nil || ce.ExpiryDuration != 1 {
		t.Errorf("customExpiryFrom(10s) = %+v, want 1 minute", ce)
	}
}
