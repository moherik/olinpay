package doku

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

const (
	testClientID     = "MCH-0001-TEST"
	testClientSecret = "SK-TEST-SECRET"
	testToken        = "test-access-token-abc123"
)

// testGateway stands in for the DOKU SNAP API. It verifies the authentication
// headers on every request (so a broken client is caught) and replays canned
// responses keyed by "METHOD /path".
type testGateway struct {
	t          *testing.T
	server     *httptest.Server
	mu         sync.Mutex
	responses  map[string]string
	tokenCalls int
	lastBody   map[string][]byte
	lastAuth   map[string]string
}

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	g := &testGateway{
		t:         t,
		responses: make(map[string]string),
		lastBody:  make(map[string][]byte),
		lastAuth:  make(map[string]string),
	}
	g.server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.server.Close)
	return g
}

func (g *testGateway) respond(method, path, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.responses[method+" "+path] = body
}

func (g *testGateway) serve(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	g.mu.Lock()
	g.lastAuth[key+" timestamp"] = r.Header.Get("X-TIMESTAMP")
	g.lastAuth[key+" signature"] = r.Header.Get("X-SIGNATURE")
	g.lastAuth[key+" clientKey"] = r.Header.Get("X-CLIENT-KEY")
	g.lastAuth[key+" partnerID"] = r.Header.Get("X-PARTNER-ID")
	g.lastAuth[key+" auth"] = r.Header.Get("Authorization")
	g.lastAuth[key+" channelID"] = r.Header.Get("CHANNEL-ID")
	body := make([]byte, 0)
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	g.lastBody[key] = body
	resp, ok := g.responses[key]
	g.mu.Unlock()

	if r.URL.Path == pathGetToken {
		g.mu.Lock()
		g.tokenCalls++
		g.mu.Unlock()
		if r.Header.Get("X-CLIENT-KEY") == "" || r.Header.Get("X-TIMESTAMP") == "" || r.Header.Get("X-SIGNATURE") == "" {
			http.Error(w, `{"responseCode":"4007300","responseMessage":"missing auth headers"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseCode":"2007300","responseMessage":"Successful","accessToken":"` + testToken + `","tokenType":"Bearer","expiresIn":900}`))
		return
	}

	// Transactional request: verify the bearer token and the HMAC signature.
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		http.Error(w, `{"responseCode":"4010000","responseMessage":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if !g.verifySignature(r, body) {
		http.Error(w, `{"responseCode":"4010000","responseMessage":"bad signature"}`, http.StatusUnauthorized)
		return
	}
	if !ok {
		http.Error(w, `{"responseCode":"4040000","responseMessage":"not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(resp))
}

// verifySignature recomputes the expected HMAC-SHA512 and compares it in
// constant time.
func (g *testGateway) verifySignature(r *http.Request, body []byte) bool {
	timestamp := r.Header.Get("X-TIMESTAMP")
	signature := r.Header.Get("X-SIGNATURE")
	if timestamp == "" || signature == "" {
		return false
	}
	sum := sha256.Sum256(body)
	hash := strings.ToLower(hex.EncodeToString(sum[:]))
	stringToSign := strings.Join([]string{r.Method, r.URL.Path, testToken, hash, timestamp}, ":")
	mac := hmac.New(sha512.New, []byte(testClientSecret))
	mac.Write([]byte(stringToSign))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// newTestProvider builds a Provider wired to the test gateway with a freshly
// generated RSA key.
func newTestProvider(t *testing.T, g *testGateway, extra map[string]string) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	cfg := provider.Config{
		"client_id":     testClientID,
		"client_secret": testClientSecret,
		"private_key":   encodeKey(t, key),
		"base_url":      g.server.URL,
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

func encodeKey(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der := x509.MarshalPKCS1PrivateKey(key)
	return base64.StdEncoding.EncodeToString(der)
}

func TestNewRequiresConfig(t *testing.T) {
	for _, key := range []string{"client_id", "client_secret", "private_key"} {
		cfg := provider.Config{
			"client_id":     testClientID,
			"client_secret": testClientSecret,
			"private_key":   "dummy",
		}
		delete(cfg, key)
		if _, err := New(cfg); err == nil {
			t.Errorf("New without %q should fail", key)
		}
	}
}

func TestCapabilities(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	caps := p.Capabilities()
	if !caps.SupportsMethod(provider.MethodVirtualAccount) || !caps.SupportsMethod(provider.MethodQRIS) || !caps.SupportsMethod(provider.MethodEWallet) {
		t.Errorf("capabilities should support VA, QRIS and e-wallet, got %v", caps.Methods)
	}
	if !caps.SupportsInquiry || !caps.SupportsCancel || !caps.SupportsWebhook || !caps.SupportsSandbox {
		t.Errorf("capabilities should advertise inquiry/cancel/webhook/sandbox, got %+v", caps)
	}
}

func TestCreateVA(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", "/virtual-accounts/bi-snap-va/v1.1/transfer-va/create-va", `{
		"responseCode":"2002500","responseMessage":"Successful",
		"virtualAccountData":{
			"partnerServiceId":"   19008","customerNo":"00000000000000000001",
			"virtualAccountNo":"   19008000000000000000000001",
			"virtualAccountName":"Test Customer","trxId":"INV-1",
			"totalAmount":{"value":"10000.00","currency":"IDR"},
			"expiredDate":"2026-10-15T10:00:00+07:00",
			"additionalInfo":{"channel":"VIRTUAL_ACCOUNT_BCA","howToPayPage":"https://pay.example/1"}
		}
	}`)
	p := newTestProvider(t, g, map[string]string{"partner_service_id": "   19008"})

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1",
		Amount:      10000,
		Currency:    provider.IDR,
		Method:      provider.MethodVirtualAccount,
		Channel:     "BCA",
		Customer:    provider.Customer{Name: "Test Customer"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "19008000000000000000000001" {
		t.Errorf("PaymentCode = %q", resp.PaymentCode)
	}
	if resp.Status != provider.StatusPending {
		t.Errorf("Status = %q, want pending", resp.Status)
	}
	if resp.Amount != 10000 {
		t.Errorf("Amount = %d, want 10000", resp.Amount)
	}
	if len(resp.Actions) != 2 {
		t.Errorf("Actions = %d, want 2 (display + how-to-pay)", len(resp.Actions))
	}
	if resp.ExpiresAt == nil {
		t.Errorf("ExpiresAt should be set")
	}

	// The request must have carried the bank channel and the signed headers.
	body := string(g.lastBody["POST /virtual-accounts/bi-snap-va/v1.1/transfer-va/create-va"])
	if !strings.Contains(body, `"channel":"VIRTUAL_ACCOUNT_BCA"`) {
		t.Errorf("request body missing channel: %s", body)
	}
	if !strings.Contains(body, `"partnerServiceId":"   19008"`) {
		t.Errorf("request body missing partnerServiceId: %s", body)
	}
	if g.lastAuth["POST /virtual-accounts/bi-snap-va/v1.1/transfer-va/create-va auth"] != "Bearer "+testToken {
		t.Errorf("Authorization header = %q", g.lastAuth["POST /virtual-accounts/bi-snap-va/v1.1/transfer-va/create-va auth"])
	}
}

func TestCreateVAUnsupportedChannel(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, map[string]string{"partner_service_id": "   19008"})
	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodVirtualAccount, Channel: "XYZ",
	})
	if err == nil {
		t.Fatal("expected error for unsupported channel")
	}
}

func TestCreateQRIS(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathQRISGenerate, `{
		"responseCode":"2005200","responseMessage":"Successful",
		"referenceNo":"REF-1","partnerReferenceNo":"INV-1",
		"qrContent":"000201010212","terminalId":"TERM1",
		"additionalInfo":{"validityPeriod":"2026-10-15T10:00:00+07:00"}
	}`)
	p := newTestProvider(t, g, map[string]string{
		"merchant_id": "M123", "terminal_id": "TERM1", "postal_code": "12345",
	})

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 25000, Method: provider.MethodQRIS, Channel: "QRIS",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "000201010212" {
		t.Errorf("PaymentCode = %q", resp.PaymentCode)
	}
	if len(resp.Actions) != 1 || resp.Actions[0].Type != provider.ActionQRCode {
		t.Errorf("Actions = %+v, want one qrcode action", resp.Actions)
	}
	body := string(g.lastBody["POST "+pathQRISGenerate])
	if !strings.Contains(body, `"merchantId":"M123"`) || !strings.Contains(body, `"postalCode":"12345"`) {
		t.Errorf("request body missing merchantId/postalCode: %s", body)
	}
}

func TestCreateEWalletDANA(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathDebitPayment1, `{
		"responseCode":"2005600","responseMessage":"Successful",
		"referenceNo":"REF-1","partnerReferenceNo":"INV-1",
		"webRedirectUrl":"https://dana.example/pay"
	}`)
	p := newTestProvider(t, g, map[string]string{"callback_url": "https://openpay.example/webhooks/doku"})

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 5000, Method: provider.MethodEWallet, Channel: "DANA",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if len(resp.Actions) != 1 || resp.Actions[0].Type != provider.ActionURL {
		t.Errorf("Actions = %+v, want one url action", resp.Actions)
	}
	body := string(g.lastBody["POST "+pathDebitPayment1])
	if !strings.Contains(body, `"channel":"EMONEY_DANA_SNAP"`) {
		t.Errorf("request body missing DANA channel: %s", body)
	}
	if !strings.Contains(body, `"urlParam":{`) {
		t.Errorf("DANA should use urlParam (singular): %s", body)
	}
}

func TestCreateEWalletShopeePay(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathDebitPayment2, `{
		"responseCode":"2005600","responseMessage":"Successful",
		"referenceNo":"REF-1","partnerReferenceNo":"INV-1",
		"webRedirectUrl":"https://shopeepay.example/pay"
	}`)
	p := newTestProvider(t, g, map[string]string{"callback_url": "https://openpay.example/webhooks/doku"})

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 5000, Method: provider.MethodEWallet, Channel: "SHOPEEPAY",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	body := string(g.lastBody["POST "+pathDebitPayment2])
	if !strings.Contains(body, `"channel":"EMONEY_SHOPEE_PAY_SNAP"`) {
		t.Errorf("request body missing ShopeePay channel: %s", body)
	}
	if !strings.Contains(body, `"urlParams":[`) {
		t.Errorf("ShopeePay should use urlParams (plural): %s", body)
	}
}

func TestInquiryVA(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathVAStatus, `{
		"responseCode":"2002600","responseMessage":"Successful",
		"virtualAccountData":{
			"paymentFlagReason":{"english":"Paid","indonesia":"Lunas"},
			"partnerServiceId":"   19008","customerNo":"00000000000000000001",
			"virtualAccountNo":"   19008000000000000000000001",
			"paidAmount":{"value":"10000.00","currency":"IDR"},
			"paymentRequestId":"PAY-1","transactionDate":"2026-10-14T10:00:00+07:00"
		},
		"additionalInfo":{"trxId":"INV-1","acquirer":{"id":"BCA"}}
	}`)
	p := newTestProvider(t, g, map[string]string{"partner_service_id": "   19008"})

	// Seed the correlation cache by creating the VA first.
	g.respond("POST", "/virtual-accounts/bi-snap-va/v1.1/transfer-va/create-va", `{
		"responseCode":"2002500","responseMessage":"Successful",
		"virtualAccountData":{"virtualAccountNo":"   19008000000000000000000001","trxId":"INV-1"}
	}`)
	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 10000, Method: provider.MethodVirtualAccount, Channel: "BCA",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	resp, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "INV-1"})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if resp.Status != provider.StatusPaid {
		t.Errorf("Status = %q, want paid", resp.Status)
	}
	if resp.Amount != 10000 {
		t.Errorf("Amount = %d, want 10000", resp.Amount)
	}
	if resp.PaidAt == nil {
		t.Errorf("PaidAt should be set")
	}
}

func TestInquiryQRIS(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathQRISQuery, `{
		"responseCode":"2005200","responseMessage":"Successful",
		"originalReferenceNo":"REF-1","originalPartnerReferenceNo":"INV-1",
		"serviceCode":"47","latestTransactionStatus":"00","transactionStatusDesc":"Success",
		"paidTime":"2026-10-14T10:00:00+07:00","amount":{"value":"25000.00","currency":"IDR"}
	}`)
	p := newTestProvider(t, g, map[string]string{"merchant_id": "M123", "terminal_id": "TERM1", "postal_code": "12345"})

	g.respond("POST", pathQRISGenerate, `{
		"responseCode":"2005200","responseMessage":"Successful",
		"referenceNo":"REF-1","partnerReferenceNo":"INV-1","qrContent":"000201010212"
	}`)
	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 25000, Method: provider.MethodQRIS,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	resp, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "INV-1"})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if resp.Status != provider.StatusPaid {
		t.Errorf("Status = %q, want paid", resp.Status)
	}
}

func TestCancelVA(t *testing.T) {
	g := newTestGateway(t)
	g.respond("DELETE", "/virtual-accounts/bi-snap-va/v1.1/transfer-va/delete-va", `{
		"responseCode":"2002500","responseMessage":"Successful",
		"virtualAccountData":{"partnerServiceId":"   19008","customerNo":"00000000000000000001","virtualAccountNo":"   19008000000000000000000001"}
	}`)
	p := newTestProvider(t, g, map[string]string{"partner_service_id": "   19008"})

	g.respond("POST", "/virtual-accounts/bi-snap-va/v1.1/transfer-va/create-va", `{
		"responseCode":"2002500","responseMessage":"Successful",
		"virtualAccountData":{"virtualAccountNo":"   19008000000000000000000001","trxId":"INV-1"}
	}`)
	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 10000, Method: provider.MethodVirtualAccount, Channel: "BCA",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-1"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusCancelled {
		t.Errorf("Status = %q, want cancelled", resp.Status)
	}
}

func TestCancelEWalletUnsupported(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, map[string]string{"callback_url": "https://openpay.example/webhooks/doku"})
	g.respond("POST", pathDebitPayment1, `{
		"responseCode":"2005600","responseMessage":"Successful","webRedirectUrl":"https://dana.example/pay"
	}`)
	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 5000, Method: provider.MethodEWallet, Channel: "DANA",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	_, err = p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-1"})
	if err == nil {
		t.Fatal("expected unsupported operation for e-wallet cancel")
	}
}

func TestParseWebhookVA(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	body := `{"partnerServiceId":"   19008","customerNo":"00000000000000000001","virtualAccountNo":"   19008000000000000000000001","virtualAccountName":"Test","trxId":"INV-1","paidAmount":{"value":"10000.00","currency":"IDR"}}`
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{
		Headers: http.Header{},
		Path:    pathVANotify,
		Body:    []byte(body),
	})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventVAPayment {
		t.Errorf("EventType = %q", evt.EventType)
	}
	if evt.ReferenceID != "INV-1" {
		t.Errorf("ReferenceID = %q", evt.ReferenceID)
	}
	if evt.ProviderReference != "19008000000000000000000001" {
		t.Errorf("ProviderReference = %q", evt.ProviderReference)
	}
	if evt.Status != provider.StatusPaid {
		t.Errorf("Status = %q, want paid", evt.Status)
	}
	if evt.Amount != 10000 {
		t.Errorf("Amount = %d, want 10000", evt.Amount)
	}
}

func TestParseWebhookDD(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	body := `{"originalPartnerReferenceNo":"INV-1","originalReferenceNo":"REF-1","originalExternalId":"EXT-1","latestTransactionStatus":"00","transactionStatusDesc":"Success","amount":{"value":"5000.00","currency":"IDR"}}`
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{
		Headers: http.Header{},
		Path:    pathDDNotify,
		Body:    []byte(body),
	})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventEWalletPayment {
		t.Errorf("EventType = %q", evt.EventType)
	}
	if evt.ReferenceID != "INV-1" {
		t.Errorf("ReferenceID = %q", evt.ReferenceID)
	}
	if evt.Status != provider.StatusPaid {
		t.Errorf("Status = %q, want paid", evt.Status)
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	body := []byte(`{"partnerServiceId":"   19008","virtualAccountNo":"VA-1","trxId":"INV-1"}`)
	timestamp := time.Now().In(jakarta).Format(iso8601Layout)
	mac := hmac.New(sha512.New, []byte(testClientSecret))
	mac.Write([]byte(notificationStringToSign(http.MethodPost, pathVANotify, testToken, body, timestamp)))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req := &provider.WebhookRequest{
		Headers: http.Header{
			"X-Signature":   []string{sig},
			"X-Timestamp":   []string{timestamp},
			"Authorization": []string{"Bearer " + testToken},
		},
		Path: pathVANotify,
		Body: body,
	}
	if err := p.VerifyWebhookSignature(req); err != nil {
		t.Errorf("VerifyWebhookSignature with valid signature: %v", err)
	}

	// Tampered body must fail.
	req.Body = []byte(`{"partnerServiceId":"   19008","virtualAccountNo":"VA-2","trxId":"INV-1"}`)
	if err := p.VerifyWebhookSignature(req); err == nil {
		t.Error("VerifyWebhookSignature should reject a tampered body")
	}
}

func TestWebhookResponse(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	status, body := p.WebhookResponse(&provider.WebhookEvent{EventType: EventVAPayment})
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if !strings.Contains(string(body), responseVAPayment) {
		t.Errorf("body = %s, want code %s", body, responseVAPayment)
	}
}

func TestTokenCached(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathQRISGenerate, `{"responseCode":"2005200","responseMessage":"Successful","qrContent":"00"}`)
	p := newTestProvider(t, g, map[string]string{"merchant_id": "M1", "terminal_id": "T1", "postal_code": "12345"})

	for i := 0; i < 3; i++ {
		_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
			ReferenceID: "INV-" + strconv.Itoa(i), Amount: 1000, Method: provider.MethodQRIS,
		})
		if err != nil {
			t.Fatalf("CreatePayment %d: %v", i, err)
		}
	}
	g.mu.Lock()
	calls := g.tokenCalls
	g.mu.Unlock()
	if calls != 1 {
		t.Errorf("token fetched %d times, want 1 (cached)", calls)
	}
}

func TestMapTransactionStatus(t *testing.T) {
	cases := map[string]provider.Status{
		"00": provider.StatusPaid,
		"01": provider.StatusPending,
		"02": provider.StatusPending,
		"03": provider.StatusPending,
		"04": provider.StatusRefunded,
		"05": provider.StatusCancelled,
		"06": provider.StatusFailed,
		"07": provider.StatusFailed,
		"99": provider.StatusUnknown,
	}
	for code, want := range cases {
		if got := mapTransactionStatus(code); got != want {
			t.Errorf("mapTransactionStatus(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestParseAndFormatAmount(t *testing.T) {
	if got, err := parseAmount("10000.00"); err != nil || got != 10000 {
		t.Errorf("parseAmount = %d, %v; want 10000, nil", got, err)
	}
	if _, err := parseAmount(""); err == nil {
		t.Error("parseAmount(\"\") should fail")
	}
	if formatAmount(10000) != "10000.00" {
		t.Errorf("formatAmount = %q", formatAmount(10000))
	}
}

func TestClassifyCode(t *testing.T) {
	if err := classifyCode("2002500"); err != nil {
		t.Errorf("classifyCode(200) = %v, want nil", err)
	}
	if err := classifyCode("4010000"); err == nil {
		t.Error("classifyCode(401) should fail")
	}
}

func TestSignGetTokenAndVerify(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	ts := time.Now().In(jakarta).Format(iso8601Layout)
	sig, err := signGetToken(key, testClientID, ts)
	if err != nil {
		t.Fatalf("signGetToken: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		t.Fatalf("signature not base64: %v", err)
	}
	digest := sha256.Sum256([]byte(getTokenStringToSign(testClientID, ts)))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], raw); err != nil {
		t.Errorf("RSA verification failed: %v", err)
	}
}
