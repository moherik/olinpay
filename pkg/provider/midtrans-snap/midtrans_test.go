package midtranssnap

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
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
	testClientID     = "MCH-MID-TEST"
	testClientSecret = "SK-MID-TEST-SECRET"
	testPartnerID    = "PARTNER-MID-TEST"
	testMerchantID   = "G123456789"
	testPartnerSvcID = "   19008"
	testToken        = "midtrans-test-token-abc123"
)

// testGateway stands in for Midtrans' BI-SNAP API. On the get-token endpoint it
// verifies the RSA-SHA256 signature with the merchant public key; on every
// transactional endpoint it verifies the bearer token and the HMAC-SHA512
// signature. Canned responses are keyed by "METHOD /path".
type testGateway struct {
	t          *testing.T
	server     *httptest.Server
	mu         sync.Mutex
	responses  map[string]string
	lastBody   map[string][]byte
	lastHeader map[string]http.Header
	tokenCalls int
	pubKey     *rsa.PublicKey
}

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	g := &testGateway{
		t:          t,
		responses:  make(map[string]string),
		lastBody:   make(map[string][]byte),
		lastHeader: make(map[string]http.Header),
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
	body, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path

	g.mu.Lock()
	g.lastBody[key] = body
	g.lastHeader[key] = r.Header.Clone()
	if r.URL.Path == pathAccessToken {
		g.tokenCalls++
	}
	resp, ok := g.responses[key]
	g.mu.Unlock()

	if r.URL.Path == pathAccessToken {
		if !g.verifyTokenSignature(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"responseCode":"4017300","responseMessage":"Unauthorized. Signature"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// expiresIn is deliberately a JSON string, matching Midtrans.
		_, _ = w.Write([]byte(`{"responseCode":"2007300","responseMessage":"Successful","accessToken":"` +
			testToken + `","tokenType":"Bearer","expiresIn":"900"}`))
		return
	}

	if r.Header.Get("Authorization") != "Bearer "+testToken {
		http.Error(w, `{"responseCode":"4015400","responseMessage":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if !g.verifySignature(r, body) {
		g.t.Errorf("gateway: bad HMAC signature on %s", key)
		http.Error(w, `{"responseCode":"4015400","responseMessage":"bad signature"}`, http.StatusUnauthorized)
		return
	}
	if !ok {
		http.Error(w, `{"responseCode":"4045400","responseMessage":"not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(resp))
}

// verifyTokenSignature checks the RSA-SHA256 signature over "clientID|timestamp".
func (g *testGateway) verifyTokenSignature(r *http.Request) bool {
	g.mu.Lock()
	pub := g.pubKey
	g.mu.Unlock()
	if pub == nil {
		return false
	}
	clientKey := r.Header.Get(headerClientID)
	ts := r.Header.Get(headerTimestamp)
	sig := r.Header.Get(headerSignature)
	if clientKey == "" || ts == "" || sig == "" {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(tokenStringToSign(clientKey, ts)))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], raw) == nil
}

// verifySignature recomputes the HMAC-SHA512 over the exact received body.
func (g *testGateway) verifySignature(r *http.Request, body []byte) bool {
	ts := r.Header.Get(headerTimestamp)
	sig := r.Header.Get(headerSignature)
	if ts == "" || sig == "" {
		return false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	want, err := signRequest(testClientSecret, r.Method, r.URL.Path, token, body, ts)
	if err != nil {
		return false
	}
	return want == sig
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

// newTestProvider builds a Provider wired to the test gateway with a freshly
// generated RSA key whose public half the gateway uses to verify get-token
// requests.
func newTestProvider(t *testing.T, g *testGateway, extra provider.Config) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	g.mu.Lock()
	g.pubKey = &key.PublicKey
	g.mu.Unlock()

	cfg := provider.Config{
		"client_id":     testClientID,
		"client_secret": testClientSecret,
		"private_key":   encodePrivateKey(key),
		"partner_id":    testPartnerID,
		"merchant_id":   testMerchantID,
		"base_url":      g.server.URL,
		"channel_id":    "12345",
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

func encodePrivateKey(key *rsa.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(key))
}

func encodePublicKey(pub *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

// --- config -----------------------------------------------------------------

func TestNewRequiresConfig(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	base := provider.Config{
		"client_id":     testClientID,
		"client_secret": testClientSecret,
		"private_key":   encodePrivateKey(key),
	}
	for _, missing := range []string{"client_id", "client_secret", "private_key"} {
		cfg := provider.Config{}
		for k, v := range base {
			cfg[k] = v
		}
		delete(cfg, missing)
		if _, err := New(cfg); err == nil {
			t.Errorf("New without %q should fail", missing)
		}
	}

	if _, err := New(provider.Config{"client_id": "a", "client_secret": "b", "private_key": "not-a-key"}); err == nil {
		t.Error("expected an error for an invalid private key")
	}
	if _, err := New(provider.Config{
		"client_id": "a", "client_secret": "b", "private_key": encodePrivateKey(key), "timeout": "nope",
	}); err == nil {
		t.Error("expected an error for an invalid timeout")
	}

	p, err := New(base)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.baseURL != defaultBaseURL {
		t.Errorf("base url = %q, want %q", p.baseURL, defaultBaseURL)
	}
	if p.partnerID != testClientID {
		t.Errorf("partner_id should default to client_id, got %q", p.partnerID)
	}
	if p.publicKey != nil {
		t.Error("public_key should be nil when not configured")
	}
}

func TestRegistryFactory(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	cfg := provider.Config{
		"client_id":     testClientID,
		"client_secret": testClientSecret,
		"private_key":   encodePrivateKey(key),
	}
	built, err := provider.Default().Build(Name, cfg)
	if err != nil {
		t.Fatalf("Build(%q): %v", Name, err)
	}
	if built.Name() != Name {
		t.Errorf("name = %q, want %q", built.Name(), Name)
	}
	if _, err := provider.NewRegistry().Build(Name, cfg); err != nil {
		t.Fatalf("Build on a fresh registry: %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	caps := p.Capabilities()

	for _, m := range []provider.Method{provider.MethodVirtualAccount, provider.MethodQRIS, provider.MethodEWallet} {
		if !caps.SupportsMethod(m) {
			t.Errorf("expected %q to be supported", m)
		}
	}
	for _, m := range []provider.Method{provider.MethodCard, provider.MethodRetailOutlet, provider.MethodPaylater, provider.MethodDirectDebit} {
		if caps.SupportsMethod(m) {
			t.Errorf("BI-SNAP does not support %q, but it was advertised", m)
		}
	}
	for _, ch := range []string{"BCA", "GOPAY", "QRIS", "bca", "  mandiri  "} {
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

func TestCreateVirtualAccount(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathVACreate, `{
		"responseCode":"2002700","responseMessage":"Successful",
		"virtualAccountData":{
			"partnerServiceId":"   19008","customerNo":"8527389546786863",
			"virtualAccountNo":"   190088527389546786863",
			"virtualAccountName":"Budi Utomo","trxId":"INV-0001",
			"totalAmount":{"value":"25000.00","currency":"IDR"},
			"expiredDate":"2026-10-15T10:00:00+07:00",
			"additionalInfo":{"merchantId":"G123456789","bank":"bca"}
		}
	}`)
	p := newTestProvider(t, g, provider.Config{"partner_service_id": testPartnerSvcID})

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0001",
		Amount:      25000,
		Currency:    provider.IDR,
		Method:      provider.MethodVirtualAccount,
		Channel:     "bca",
		Customer:    provider.Customer{Name: "Budi Utomo", Email: "budi@example.com"},
		Expiry:      time.Hour,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "190088527389546786863" {
		t.Errorf("PaymentCode = %q", resp.PaymentCode)
	}
	if resp.ProviderReference != "190088527389546786863" {
		t.Errorf("ProviderReference = %q", resp.ProviderReference)
	}
	if resp.Status != provider.StatusPending {
		t.Errorf("Status = %q, want pending", resp.Status)
	}
	if resp.Amount != 25000 {
		t.Errorf("Amount = %d, want 25000", resp.Amount)
	}
	if resp.ExpiresAt == nil {
		t.Error("expected an expiry")
	}
	if len(resp.Actions) != 1 || resp.Actions[0].Type != provider.ActionDisplay {
		t.Errorf("Actions = %+v, want one display action", resp.Actions)
	}

	body := g.body(t, http.MethodPost, pathVACreate)
	if body["partnerServiceId"] != testPartnerSvcID {
		t.Errorf("partnerServiceId = %v", body["partnerServiceId"])
	}
	if body["trxId"] != "INV-0001" {
		t.Errorf("trxId = %v", body["trxId"])
	}
	total, _ := body["totalAmount"].(map[string]any)
	if total["value"] != "25000.00" || total["currency"] != "IDR" {
		t.Errorf("totalAmount = %v", total)
	}
	add, _ := body["additionalInfo"].(map[string]any)
	if add["bank"] != "bca" {
		t.Errorf("additionalInfo.bank = %v, want bca", add["bank"])
	}

	if got := g.header(t, http.MethodPost, pathVACreate, headerPartnerID); got != testPartnerID {
		t.Errorf("X-PARTNER-ID = %q", got)
	}
	if got := g.header(t, http.MethodPost, pathVACreate, headerExternalID); got != "INV-0001" {
		t.Errorf("X-EXTERNAL-ID = %q, want the trxId %q", got, "INV-0001")
	}
	if got := g.header(t, http.MethodPost, pathVACreate, headerClientID); got != testClientID {
		t.Errorf("X-CLIENT-KEY = %q", got)
	}
	if got := g.header(t, http.MethodPost, pathVACreate, headerChannelID); got != "12345" {
		t.Errorf("CHANNEL-ID = %q", got)
	}
}

func TestCreateVirtualAccountMandiriLabels(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathVACreate, `{
		"responseCode":"2002700","responseMessage":"Successful",
		"virtualAccountData":{"virtualAccountNo":"   190088527389546786863","trxId":"INV-0002"}
	}`)
	p := newTestProvider(t, g, provider.Config{"partner_service_id": testPartnerSvcID})

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0002", Amount: 44000, Method: provider.MethodVirtualAccount, Channel: "MANDIRI",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	body := g.body(t, http.MethodPost, pathVACreate)
	add, _ := body["additionalInfo"].(map[string]any)
	mandiri, _ := add["mandiri"].(map[string]any)
	if mandiri["billInfo1"] != "Payment" {
		t.Errorf("mandiri.billInfo1 = %v", mandiri["billInfo1"])
	}
}

func TestCreateVirtualAccountErrors(t *testing.T) {
	g := newTestGateway(t)

	// Missing channel.
	p := newTestProvider(t, g, provider.Config{"partner_service_id": testPartnerSvcID})
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodVirtualAccount,
	}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest for a missing channel, got %v", err)
	}

	// Unsupported channel.
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodVirtualAccount, Channel: "XYZ",
	}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest for an unknown channel, got %v", err)
	}

	// Missing partner_service_id.
	p2 := newTestProvider(t, g, nil)
	if _, err := p2.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodVirtualAccount, Channel: "BCA",
	}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest without partner_service_id, got %v", err)
	}
}

func TestCreateQRIS(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathQRISGenerate, `{
		"responseCode":"2005200","responseMessage":"Successful",
		"referenceNo":"REF-1","partnerReferenceNo":"INV-1",
		"qrContent":"00020101021226610014COM.GO-JEK.WWW","qrUrl":"https://api.midtrans.com/v4/qris/qr-code",
		"additionalInfo":{"acquirer":"gopay"}
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 275000, Method: provider.MethodQRIS, Channel: "QRIS",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "00020101021226610014COM.GO-JEK.WWW" {
		t.Errorf("PaymentCode = %q", resp.PaymentCode)
	}
	if resp.Status != provider.StatusPending || resp.Amount != 275000 {
		t.Errorf("status/amount = %q/%d", resp.Status, resp.Amount)
	}
	if len(resp.Actions) != 2 {
		t.Fatalf("Actions = %+v, want a QR code and a URL", resp.Actions)
	}
	if resp.Actions[0].Type != provider.ActionQRCode || resp.Actions[1].Type != provider.ActionURL {
		t.Errorf("action types = %q, %q", resp.Actions[0].Type, resp.Actions[1].Type)
	}

	body := g.body(t, http.MethodPost, pathQRISGenerate)
	if body["merchantId"] != testMerchantID {
		t.Errorf("merchantId = %v", body["merchantId"])
	}
	add, _ := body["additionalInfo"].(map[string]any)
	if add["acquirer"] != "gopay" {
		t.Errorf("additionalInfo.acquirer = %v, want gopay", add["acquirer"])
	}
}

func TestCreateEWallet(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathDebitCreate, `{
		"responseCode":"2005600","responseMessage":"Successful",
		"referenceNo":"REF-2","partnerReferenceNo":"INV-2",
		"webRedirectUrl":"https://merchants.sbx.midtrans.com/web/redirect",
		"appRedirectUrl":"gopay://pay?tref=abc"
	}`)
	p := newTestProvider(t, g, nil)

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-2",
		Amount:      10000,
		Method:      provider.MethodEWallet,
		Channel:     "GOPAY",
		Customer:    provider.Customer{Name: "Budi Utomo", Email: "budi@example.com"},
		Metadata:    map[string]string{"return_url": "https://shop.example/checkout/return"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.ProviderReference != "REF-2" || resp.ReferenceID != "INV-2" {
		t.Errorf("references = %q/%q", resp.ProviderReference, resp.ReferenceID)
	}
	if len(resp.Actions) != 2 {
		t.Fatalf("Actions = %+v, want a deeplink and a URL", resp.Actions)
	}
	if resp.Actions[0].Type != provider.ActionDeeplink || resp.Actions[1].Type != provider.ActionURL {
		t.Errorf("action types = %q, %q", resp.Actions[0].Type, resp.Actions[1].Type)
	}

	body := g.body(t, http.MethodPost, pathDebitCreate)
	if body["chargeToken"] != testToken {
		t.Errorf("chargeToken = %v, want the access token", body["chargeToken"])
	}
	opts, _ := body["payOptionDetails"].([]any)
	if len(opts) != 1 {
		t.Fatalf("payOptionDetails = %v", opts)
	}
	opt, _ := opts[0].(map[string]any)
	if opt["payMethod"] != "GOPAY" || opt["payOption"] != "GOPAY" {
		t.Errorf("payOptionDetails[0] = %v", opt)
	}
	params, _ := body["urlParams"].([]any)
	if len(params) != 1 {
		t.Fatalf("urlParams = %v", params)
	}
	param, _ := params[0].(map[string]any)
	if param["url"] != "https://shop.example/checkout/return" || param["type"] != "PAY_RETURN" {
		t.Errorf("urlParams[0] = %v", param)
	}
}

func TestCreateUnsupportedMethodAndCurrency(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodCard,
	}); !errors.Is(err, provider.ErrUnsupportedOperation) {
		t.Errorf("expected ErrUnsupportedOperation for a card, got %v", err)
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
}

func TestCreateSurfacesGatewayError(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathQRISGenerate, `{"responseCode":"4095201","responseMessage":"Transaction has previously been processed"}`)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 1000, Method: provider.MethodQRIS,
	})
	if !errors.Is(err, provider.ErrDuplicateReference) {
		t.Fatalf("expected ErrDuplicateReference, got %v", err)
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Code != "4095201" {
		t.Errorf("expected the gateway code to be preserved, got %#v", err)
	}
}

// --- Inquiry ----------------------------------------------------------------

func TestInquiryVA(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathVACreate, `{
		"responseCode":"2002700","responseMessage":"Successful",
		"virtualAccountData":{"virtualAccountNo":"   19008000000000000123","trxId":"INV-1"}
	}`)
	g.respond("POST", pathVAStatus, `{
		"responseCode":"2002600","responseMessage":"Successful",
		"virtualAccountData":{
			"paymentFlagStatus":"00",
			"paymentFlagReason":{"english":"Success","indonesia":"Sukses"},
			"partnerServiceId":"   19008","customerNo":"000000000000000123",
			"virtualAccountNo":"   19008000000000000123",
			"inquiryRequestId":"INV-1","paidAmount":{"value":"10000.00","currency":"IDR"},
			"totalAmount":{"value":"10000.00","currency":"IDR"},
			"transactionDate":"2026-10-14T10:00:00+07:00"
		}
	}`)
	p := newTestProvider(t, g, provider.Config{"partner_service_id": testPartnerSvcID})

	seedVA(t, p)

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
		t.Error("expected paid_at")
	}

	body := g.body(t, http.MethodPost, pathVAStatus)
	if body["virtualAccountNo"] != "19008000000000000123" {
		t.Errorf("virtualAccountNo = %v", body["virtualAccountNo"])
	}
	if body["inquiryRequestId"] != "INV-1" {
		t.Errorf("inquiryRequestId = %v", body["inquiryRequestId"])
	}
}

func TestInquiryQRIS(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathQRISGenerate, `{
		"responseCode":"2005200","responseMessage":"Successful",
		"referenceNo":"REF-1","partnerReferenceNo":"INV-1","qrContent":"000201010212"
	}`)
	g.respond("POST", pathQRISQuery, `{
		"responseCode":"2005200","responseMessage":"Successful",
		"originalReferenceNo":"REF-1","originalPartnerReferenceNo":"INV-1",
		"serviceCode":"47","latestTransactionStatus":"00","transactionStatusDesc":"Success",
		"paidTime":"2026-10-14T10:00:00+07:00","amount":{"value":"25000.00","currency":"IDR"}
	}`)
	p := newTestProvider(t, g, nil)

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
	if resp.Status != provider.StatusPaid || resp.Amount != 25000 {
		t.Errorf("status/amount = %q/%d", resp.Status, resp.Amount)
	}

	createExt := g.header(t, http.MethodPost, pathQRISGenerate, headerExternalID)
	body := g.body(t, http.MethodPost, pathQRISQuery)
	if body["serviceCode"] != qrisServiceCode {
		t.Errorf("serviceCode = %v, want %q", body["serviceCode"], qrisServiceCode)
	}
	if body["originalExternalId"] != createExt {
		t.Errorf("originalExternalId = %v, want the create X-EXTERNAL-ID %q", body["originalExternalId"], createExt)
	}
	if queryExt := g.header(t, http.MethodPost, pathQRISQuery, headerExternalID); queryExt == createExt {
		t.Error("X-EXTERNAL-ID must differ between create and query")
	}
}

func TestInquiryEWallet(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathDebitCreate, `{
		"responseCode":"2005600","responseMessage":"Successful",
		"referenceNo":"REF-2","partnerReferenceNo":"INV-2","webRedirectUrl":"https://x.example/pay"
	}`)
	g.respond("POST", pathDebitStatus, `{
		"responseCode":"2005500","responseMessage":"Successful",
		"originalReferenceNo":"REF-2","originalPartnerReferenceNo":"INV-2",
		"serviceCode":"54","latestTransactionStatus":"00",
		"transAmount":{"value":"5000.00","currency":"IDR"},"paidTime":"2026-10-14T10:05:00+07:00"
	}`)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-2", Amount: 5000, Method: provider.MethodEWallet, Channel: "GOPAY",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	resp, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "INV-2"})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if resp.Status != provider.StatusPaid || resp.Amount != 5000 {
		t.Errorf("status/amount = %q/%d", resp.Status, resp.Amount)
	}

	createExt := g.header(t, http.MethodPost, pathDebitCreate, headerExternalID)
	body := g.body(t, http.MethodPost, pathDebitStatus)
	if body["serviceCode"] != debitServiceCode {
		t.Errorf("serviceCode = %v, want %q", body["serviceCode"], debitServiceCode)
	}
	if body["originalExternalId"] != createExt {
		t.Errorf("originalExternalId = %v, want %q", body["originalExternalId"], createExt)
	}
}

func TestInquiryUnknownReference(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "nope"})
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := p.Inquiry(context.Background(), &provider.InquiryRequest{}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for an empty request, got %v", err)
	}
}

// --- Cancel -----------------------------------------------------------------

func TestCancelVA(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathVACreate, `{
		"responseCode":"2002700","responseMessage":"Successful",
		"virtualAccountData":{"virtualAccountNo":"   19008000000000000123","trxId":"INV-1"}
	}`)
	g.respond("POST", pathVADelete, `{
		"responseCode":"2003100","responseMessage":"Successful",
		"virtualAccountData":{"virtualAccountNo":"   19008000000000000123","trxId":"INV-1"}
	}`)
	p := newTestProvider(t, g, provider.Config{"partner_service_id": testPartnerSvcID})

	seedVA(t, p)

	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-1"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusCancelled {
		t.Errorf("Status = %q, want cancelled", resp.Status)
	}
	if !g.hasCalled(http.MethodPost, pathVADelete) {
		t.Error("expected a delete-va call")
	}
}

func TestCancelQRIS(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathQRISGenerate, `{
		"responseCode":"2005200","responseMessage":"Successful",
		"referenceNo":"REF-1","partnerReferenceNo":"INV-1","qrContent":"000201010212"
	}`)
	g.respond("POST", pathQRISCancel, `{
		"responseCode":"2007700","responseMessage":"Successful",
		"originalReferenceNo":"REF-1","cancelTime":"2026-10-14T11:00:00+07:00"
	}`)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 25000, Method: provider.MethodQRIS,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	createExt := g.header(t, http.MethodPost, pathQRISGenerate, headerExternalID)
	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-1", Reason: "expire"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusCancelled {
		t.Errorf("Status = %q, want cancelled", resp.Status)
	}
	body := g.body(t, http.MethodPost, pathQRISCancel)
	if body["reason"] != "expire" {
		t.Errorf("reason = %v, want expire", body["reason"])
	}
	if body["originalExternalId"] != createExt {
		t.Errorf("originalExternalId = %v, want %q", body["originalExternalId"], createExt)
	}
}

func TestCancelUnknownReference(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if _, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "nope"}); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestCreateEWalletWithoutReturnURL verifies the e-wallet PAY_RETURN url is
// omitted (not taken from any callback configuration) when the payment request
// does not supply metadata["return_url"].
func TestCreateEWalletWithoutReturnURL(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathDebitCreate, `{
		"responseCode":"2005600","responseMessage":"Successful",
		"referenceNo":"REF-2","partnerReferenceNo":"INV-2"
	}`)
	p := newTestProvider(t, g, nil)

	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-2", Amount: 10000, Method: provider.MethodEWallet, Channel: "GOPAY",
	}); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	body := g.body(t, http.MethodPost, pathDebitCreate)
	if params, ok := body["urlParams"]; ok {
		t.Errorf("urlParams = %v, want omitted without metadata return_url", params)
	}
}

// TestQRISSubMerchantID verifies the per-payment QRIS merchant id supplied via
// metadata["sub_merchant_id"] addresses the generate call and is reused for the
// matching status query and cancel.
func TestQRISSubMerchantID(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathQRISGenerate, `{
		"responseCode":"2005200","responseMessage":"Successful",
		"referenceNo":"REF-1","partnerReferenceNo":"INV-1","qrContent":"000201010212"
	}`)
	g.respond("POST", pathQRISQuery, `{
		"responseCode":"2005200","responseMessage":"Successful",
		"originalReferenceNo":"REF-1","originalPartnerReferenceNo":"INV-1",
		"serviceCode":"47","latestTransactionStatus":"00","amount":{"value":"25000.00","currency":"IDR"}
	}`)
	g.respond("POST", pathQRISCancel, `{
		"responseCode":"2007700","responseMessage":"Successful",
		"originalReferenceNo":"REF-1"
	}`)
	p := newTestProvider(t, g, nil)

	const subMerchant = "00007100010926"
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 25000, Method: provider.MethodQRIS,
		Metadata: map[string]string{"sub_merchant_id": subMerchant},
	}); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if got := g.body(t, http.MethodPost, pathQRISGenerate)["merchantId"]; got != subMerchant {
		t.Errorf("generate merchantId = %v, want %q", got, subMerchant)
	}

	if _, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "INV-1"}); err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if got := g.body(t, http.MethodPost, pathQRISQuery)["merchantId"]; got != subMerchant {
		t.Errorf("query merchantId = %v, want %q", got, subMerchant)
	}

	if _, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-1"}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := g.body(t, http.MethodPost, pathQRISCancel)["merchantId"]; got != subMerchant {
		t.Errorf("cancel merchantId = %v, want %q", got, subMerchant)
	}
}

// --- ParseWebhook -----------------------------------------------------------

func TestParseWebhookVA(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	body := []byte(`{
		"partnerServiceId":"   19008","customerNo":"000000000000000123",
		"virtualAccountNo":"   19008000000000000123","virtualAccountName":"Test",
		"trxId":"INV-1","paymentRequestId":"PAY-1",
		"paidAmount":{"value":"10000.00","currency":"IDR"},
		"referenceNo":"REF-1",
		"additionalInfo":{"bank":"BCA","paymentFlagStatus":"00","merchantId":"G123456789","paidTime":"2026-10-14T10:00:00+07:00"}
	}`)
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Path: pathVANotify, Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventVAPayment {
		t.Errorf("EventType = %q", evt.EventType)
	}
	if evt.ReferenceID != "INV-1" {
		t.Errorf("ReferenceID = %q", evt.ReferenceID)
	}
	if evt.ProviderReference != "19008000000000000123" {
		t.Errorf("ProviderReference = %q", evt.ProviderReference)
	}
	if evt.Status != provider.StatusPaid {
		t.Errorf("Status = %q, want paid", evt.Status)
	}
	if evt.Amount != 10000 {
		t.Errorf("Amount = %d, want 10000", evt.Amount)
	}
	if evt.PaidAt == nil {
		t.Error("expected paid_at")
	}
}

func TestParseWebhookQRIS(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	body := []byte(`{
		"originalReferenceNo":"REF-1","originalPartnerReferenceNo":"INV-1",
		"latestTransactionStatus":"00","transactionStatusDesc":"success",
		"amount":{"value":"25000.00","currency":"IDR"},"externalStoreID":"STORE-1",
		"additionalInfo":{"qris":{"issuerName":"gopay","acquirerName":"gopay","transactionType":"ON-US"}}
	}`)
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Path: pathQRISNotify, Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventQRISPayment {
		t.Errorf("EventType = %q", evt.EventType)
	}
	if evt.ReferenceID != "INV-1" || evt.ProviderReference != "REF-1" {
		t.Errorf("references = %q/%q", evt.ReferenceID, evt.ProviderReference)
	}
	if evt.Status != provider.StatusPaid || evt.Amount != 25000 {
		t.Errorf("status/amount = %q/%d", evt.Status, evt.Amount)
	}
}

func TestParseWebhookEWallet(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	body := []byte(`{
		"createdTime":"2026-10-14T10:00:00+07:00","latestTransactionStatus":"00",
		"originalReferenceNo":"REF-2","finishedTime":"2026-10-14T10:05:00+07:00",
		"originalPartnerReferenceNo":"INV-2","transactionStatusDesc":"success",
		"originalExternalId":"1234567890","amount":{"value":"5000.00","currency":"IDR"},
		"additionalInfo":{"userPaymentDetails":[{"payMethod":"gopay","payOption":"GOPAY_WALLET"}]}
	}`)
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Path: pathDBNotify, Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventEWalletPayment {
		t.Errorf("EventType = %q", evt.EventType)
	}
	if evt.ReferenceID != "INV-2" || evt.ProviderReference != "REF-2" {
		t.Errorf("references = %q/%q", evt.ReferenceID, evt.ProviderReference)
	}
	if evt.Status != provider.StatusPaid || evt.Amount != 5000 {
		t.Errorf("status/amount = %q/%d", evt.Status, evt.Amount)
	}
	if evt.PaidAt == nil {
		t.Error("expected paid_at from finishedTime")
	}
}

func TestParseWebhookStatuses(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	cases := []struct {
		status string
		want   provider.Status
	}{
		{"03", provider.StatusPending},
		{"05", provider.StatusCancelled},
		{"06", provider.StatusFailed},
		{"08", provider.StatusExpired},
	}
	for _, tc := range cases {
		body := []byte(`{"originalReferenceNo":"REF","latestTransactionStatus":"` + tc.status + `","additionalInfo":{"qris":{"issuerName":"x"}}}`)
		evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Path: pathQRISNotify, Body: body})
		if err != nil {
			t.Fatalf("ParseWebhook(%s): %v", tc.status, err)
		}
		if evt.Status != tc.want {
			t.Errorf("status %s => %q, want %q", tc.status, evt.Status, tc.want)
		}
		if evt.PaidAt != nil {
			t.Errorf("status %s must not carry paid_at", tc.status)
		}
	}
}

func TestParseWebhookUnknownShape(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if _, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: []byte(`{"foo":"bar"}`)}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
	if _, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest for an empty body, got %v", err)
	}
}

// --- VerifyWebhookSignature -------------------------------------------------

func TestVerifyWebhookSignature(t *testing.T) {
	g := newTestGateway(t)
	midKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate midtrans key: %v", err)
	}
	pub, err := encodePublicKey(&midKey.PublicKey)
	if err != nil {
		t.Fatalf("encode public key: %v", err)
	}
	p := newTestProvider(t, g, provider.Config{"public_key": pub})

	body := []byte(`{"partnerServiceId":"   19008","virtualAccountNo":"VA-1","trxId":"INV-1"}`)
	timestamp := time.Now().In(jakarta).Format(iso8601Layout)

	// Midtrans signs over the documented notification path, while our callback
	// arrives on /webhooks/midtrans-snap; verification must try both.
	sign := func(body []byte, endpoint string) string {
		canonical, err := minify(body)
		if err != nil {
			t.Fatalf("minify: %v", err)
		}
		digest := sha256.Sum256([]byte(notificationStringToSign(http.MethodPost, endpoint, canonical, timestamp)))
		sig, err := rsa.SignPKCS1v15(rand.Reader, midKey, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return base64.StdEncoding.EncodeToString(sig)
	}

	req := &provider.WebhookRequest{
		Headers: http.Header{},
		Path:    "/webhooks/midtrans-snap",
		Body:    body,
	}
	req.Headers.Set(headerSignature, sign(body, pathVANotify))
	req.Headers.Set(headerTimestamp, timestamp)
	if err := p.VerifyWebhookSignature(req); err != nil {
		t.Errorf("valid signature (documented endpoint) rejected: %v", err)
	}

	req.Headers.Set(headerSignature, sign(body, "/webhooks/midtrans-snap"))
	if err := p.VerifyWebhookSignature(req); err != nil {
		t.Errorf("valid signature (received path) rejected: %v", err)
	}

	req.Headers.Set(headerSignature, sign([]byte(`{"tampered":true}`), pathVANotify))
	if err := p.VerifyWebhookSignature(req); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for a tampered body, got %v", err)
	}

	req.Headers.Set(headerSignature, "not-base64!!")
	if err := p.VerifyWebhookSignature(req); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for a malformed signature, got %v", err)
	}

	req.Headers.Del(headerTimestamp)
	if err := p.VerifyWebhookSignature(req); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for a missing header, got %v", err)
	}
}

func TestVerifyWebhookSignatureRequiresPublicKey(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil) // no public_key

	err := p.VerifyWebhookSignature(&provider.WebhookRequest{
		Headers: http.Header{headerSignature: {"x"}, headerTimestamp: {"y"}},
		Body:    []byte(`{}`),
	})
	if !errors.Is(err, provider.ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature without public_key, got %v", err)
	}
}

func TestWebhookResponse(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	for _, tc := range []struct {
		event string
		code  string
	}{
		{EventVAPayment, responseVAPayment},
		{EventQRISPayment, responseQRISPayment},
		{EventEWalletPayment, responseEWalletPayment},
	} {
		status, body := p.WebhookResponse(&provider.WebhookEvent{EventType: tc.event})
		if status != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", tc.event, status)
		}
		if !strings.Contains(string(body), tc.code) {
			t.Errorf("%s: body = %s, want code %s", tc.event, body, tc.code)
		}
	}
}

// --- token caching ----------------------------------------------------------

func TestTokenCached(t *testing.T) {
	g := newTestGateway(t)
	g.respond("POST", pathQRISGenerate, `{"responseCode":"2005200","responseMessage":"Successful","qrContent":"00"}`)
	p := newTestProvider(t, g, nil)

	for i := 0; i < 3; i++ {
		if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
			ReferenceID: "INV-" + strconv.Itoa(i), Amount: 1000, Method: provider.MethodQRIS,
		}); err != nil {
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

// --- mapping and helpers ----------------------------------------------------

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
		"08": provider.StatusExpired,
		"09": provider.StatusFailed,
		"99": provider.StatusUnknown,
		"":   provider.StatusUnknown,
	}
	for code, want := range cases {
		if got := mapTransactionStatus(code); got != want {
			t.Errorf("mapTransactionStatus(%q) = %q, want %q", code, got, want)
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
		{"", 0, true},
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

func TestFormatAmountAndPayload(t *testing.T) {
	if formatAmount(10000) != "10000.00" {
		t.Errorf("formatAmount = %q", formatAmount(10000))
	}
	payload := amountPayloadOf(25000)
	if payload.Value != "25000.00" || payload.Currency != "IDR" {
		t.Errorf("amountPayloadOf = %+v", payload)
	}
}

func TestClassifyCode(t *testing.T) {
	cases := map[string]error{
		"2002700": nil,
		"4015400": provider.ErrUnauthorized,
		"4045400": provider.ErrNotFound,
		"4095201": provider.ErrDuplicateReference,
		"4005400": provider.ErrInvalidRequest,
		"5005400": provider.ErrGateway,
	}
	for code, want := range cases {
		if got := classifyCode(code); got != want {
			t.Errorf("classifyCode(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestParseTime(t *testing.T) {
	tm := parseTime("2026-10-14T10:00:00+07:00")
	if tm == nil {
		t.Fatal("expected a parsed time")
	}
	if _, offset := tm.Zone(); offset != 7*60*60 {
		t.Errorf("offset = %d, want +7h", offset)
	}
	if parseTime("") != nil || parseTime("not-a-time") != nil {
		t.Error("empty or unparseable values must yield nil")
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

func TestCustomerNoIsDeterministicAndNumeric(t *testing.T) {
	a := customerNo("INV-0001")
	b := customerNo("INV-0001")
	if a != b {
		t.Errorf("customerNo is not deterministic: %q vs %q", a, b)
	}
	if a == customerNo("INV-0002") {
		t.Error("different references should produce different customer numbers")
	}
	if !isDigits(a) || len(a) != 10 {
		t.Errorf("customerNo = %q, want 10 digits", a)
	}
}

func TestExternalIDIsUniqueAndNumeric(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := externalID()
		if !isDigits(id) {
			t.Fatalf("externalID = %q, want a numeric string", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("externalID produced a duplicate: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestExpiryDefaultAndOverride(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if got := p.expiry(0); time.Until(got) > defaultExpiry || time.Until(got) < defaultExpiry-time.Minute {
		t.Errorf("expiry(0) = %v, want ~%v from now", got, defaultExpiry)
	}
	if got := p.expiry(2 * time.Hour); time.Until(got) > 2*time.Hour || time.Until(got) < 2*time.Hour-time.Minute {
		t.Errorf("expiry(2h) = %v, want ~2h from now", got)
	}
	if got := p.expiry(maxExpiry + 48*time.Hour); time.Until(got) > maxExpiry {
		t.Errorf("expiry should clamp to %v", maxExpiry)
	}
}

func TestFlexInt(t *testing.T) {
	cases := map[string]flexInt{
		`900`:    900,
		`"900"`:  900,
		`null`:   0,
		`""`:     0,
		`"1800"`: 1800,
	}
	for raw, want := range cases {
		var got flexInt
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Errorf("flexInt(%s): %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("flexInt(%s) = %d, want %d", raw, got, want)
		}
	}
	if err := json.Unmarshal([]byte(`"nope"`), new(flexInt)); err == nil {
		t.Error("expected an error for a non-numeric string")
	}
}

// --- helpers ----------------------------------------------------------------

func seedVA(t *testing.T, p *Provider) {
	t.Helper()
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-1", Amount: 10000, Method: provider.MethodVirtualAccount, Channel: "BCA",
	}); err != nil {
		t.Fatalf("seed VA: %v", err)
	}
}

func (g *testGateway) hasCalled(method, path string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.lastBody[method+" "+path]
	return ok
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
