package winpay

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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

// sharedKey generates the RSA key used across tests once per process; 2048-bit
// generation is not cheap enough to repeat for every test.
var (
	sharedKeyOnce sync.Once
	sharedKeyVal  *rsa.PrivateKey
)

func sharedKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	sharedKeyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		sharedKeyVal = key
	})
	return sharedKeyVal
}

// testGateway is a stand-in for the Winpay SNAP API. It verifies the signature
// on every request it receives (so a broken signer is caught) and replays
// canned responses.
type testGateway struct {
	t         *testing.T
	key       *rsa.PrivateKey
	server    *httptest.Server
	mu        sync.Mutex
	responses map[string]string
	requests  map[string][]byte
}

func newTestGateway(t *testing.T) *testGateway {
	t.Helper()
	g := &testGateway{
		t:         t,
		key:       sharedKey(t),
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
	compact, err := minify(body)
	if err != nil {
		g.t.Errorf("gateway: minify: %v", err)
	}
	if err := verifySignature(&g.key.PublicKey, r.Method, r.URL.Path, compact,
		r.Header.Get(headerTimestamp), r.Header.Get(headerSignature)); err != nil {
		g.t.Errorf("gateway: rejected signature on %s %s: %v", r.Method, r.URL.Path, err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	for _, header := range []string{headerPartnerID, headerExternalID, headerChannelID} {
		if r.Header.Get(header) == "" {
			g.t.Errorf("gateway: request to %s is missing %s", r.URL.Path, header)
		}
	}

	g.mu.Lock()
	g.requests[r.Method+" "+r.URL.Path] = compact
	response, ok := g.responses[r.URL.Path]
	g.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNotFound)
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
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("gateway request body is not JSON: %v", err)
	}
	return out
}

func newTestProvider(t *testing.T, g *testGateway, extra provider.Config) *Provider {
	t.Helper()
	cfg := provider.Config{
		"partner_id":  "962489e9-de5d-4eb7-92a4-b07d44d64bf4",
		"private_key": privateKeyPEM(g.key),
		"base_url":    g.server.URL,
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

func privateKeyPEM(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func publicKeyPEM(t *testing.T, key *rsa.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("marshalling public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// signatureHeaders builds the SNAP headers the way net/http would deliver them
// (canonical capitalization), which is what the provider reads.
func signatureHeaders(timestamp, signature string) http.Header {
	headers := http.Header{}
	headers.Set(headerTimestamp, timestamp)
	headers.Set(headerSignature, signature)
	return headers
}

// --- CreatePayment ---------------------------------------------------------

func TestCreateVirtualAccount(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathCreateVA, `{
		"responseCode": "2002700",
		"responseMessage": "Success",
		"virtualAccountData": {
			"partnerServiceId": "   22691",
			"customerNo": "41693898987",
			"virtualAccountNo": "   2269141693898987",
			"virtualAccountName": "Chus Pandi",
			"trxId": "INV-0001",
			"totalAmount": {"value": "25000.00", "currency": "IDR"},
			"virtualAccountTrxType": "c",
			"expiredDate": "2023-09-05T19:30:14+07:00",
			"additionalInfo": {"channel": "CIMB", "contractId": "cia80bff69-1073-4811-b1e1-13b738784d8b"}
		}
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0001",
		Amount:      25000,
		Method:      provider.MethodVirtualAccount,
		Channel:     "cimb",
		Customer:    provider.Customer{Name: "Chus Pandi"},
		Expiry:      time.Hour,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}

	if resp.ProviderReference != "cia80bff69-1073-4811-b1e1-13b738784d8b" {
		t.Errorf("provider reference = %q", resp.ProviderReference)
	}
	if resp.PaymentCode != "2269141693898987" {
		t.Errorf("payment code = %q, want the trimmed VA number", resp.PaymentCode)
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
	if len(resp.Actions) != 1 || resp.Actions[0].Type != provider.ActionDisplay {
		t.Errorf("actions = %+v, want a single display action", resp.Actions)
	}

	sent := g.request(t, http.MethodPost, pathCreateVA)
	if sent["trxId"] != "INV-0001" {
		t.Errorf("trxId sent = %v", sent["trxId"])
	}
	if sent["virtualAccountTrxType"] != "c" {
		t.Errorf("virtualAccountTrxType = %v, want c", sent["virtualAccountTrxType"])
	}
	additional, _ := sent["additionalInfo"].(map[string]any)
	if additional["channel"] != "CIMB" {
		t.Errorf("channel sent = %v, want CIMB (upper-cased)", additional["channel"])
	}
	amount, _ := sent["totalAmount"].(map[string]any)
	if amount["value"] != "25000.00" || amount["currency"] != "IDR" {
		t.Errorf("totalAmount sent = %v", amount)
	}
	if sent["expiredDate"] == "" || sent["expiredDate"] == nil {
		t.Error("expected an expiredDate to be sent")
	}
}

func TestCreateVirtualAccountOpenRecurringNeedsCustomerNo(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0002",
		Amount:      10000,
		Method:      provider.MethodVirtualAccount,
		Channel:     "BCA",
		Metadata:    map[string]string{"va_trx_type": "o"},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCreateQRISDynamic(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathQRISGenerate, `{
		"responseCode": "2004700",
		"responseMessage": "Success",
		"partnerReferenceNo": "ref-1",
		"qrContent": null,
		"qrUrl": "https://sandbox-payment.winpay.id/scqr/get_image_qr?payid=abc",
		"terminalId": "TERM GIGIH",
		"additionalInfo": {
			"contractId": "qr2300a3fe-68b8-414b-bf00-8f44ce1cf5d3",
			"expiredAt": "2023-09-06T23:08:56+07:00",
			"isStatic": false
		}
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "ref-1",
		Amount:      50000,
		Method:      provider.MethodQRIS,
		Expiry:      2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.ProviderReference != "qr2300a3fe-68b8-414b-bf00-8f44ce1cf5d3" {
		t.Errorf("provider reference = %q", resp.ProviderReference)
	}
	if resp.PaymentCode != "https://sandbox-payment.winpay.id/scqr/get_image_qr?payid=abc" {
		t.Errorf("payment code = %q, want the QR url fallback", resp.PaymentCode)
	}
	if resp.ExpiresAt == nil {
		t.Error("expected expiry from additionalInfo.expiredAt")
	}
	if len(resp.Actions) != 1 || resp.Actions[0].Type != provider.ActionURL {
		t.Errorf("actions = %+v, want a single url action (qrContent was null)", resp.Actions)
	}

	sent := g.request(t, http.MethodPost, pathQRISGenerate)
	additional, _ := sent["additionalInfo"].(map[string]any)
	if additional["isStatic"] != false {
		t.Errorf("isStatic = %v, want false", additional["isStatic"])
	}
	if sent["validityPeriod"] == nil {
		t.Error("expected validityPeriod for a dynamic QR")
	}
}

func TestCreateQRISPublishesQRContent(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathQRISGenerate, `{
		"responseCode": "2004700",
		"responseMessage": "Success",
		"partnerReferenceNo": "ref-1",
		"qrContent": "00020101021126610014COM.GO-JEK.WWW",
		"qrUrl": "https://sandbox-payment.winpay.id/scqr/get_image_qr?payid=abc",
		"additionalInfo": {"contractId": "qr-1", "expiredAt": "2023-09-06T23:08:56+07:00", "isStatic": false}
	}`)

	p := newTestProvider(t, g, nil)
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "ref-1", Amount: 50000, Method: provider.MethodQRIS,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PaymentCode != "00020101021126610014COM.GO-JEK.WWW" {
		t.Errorf("payment code = %q, want the QR payload", resp.PaymentCode)
	}
	if len(resp.Actions) != 2 {
		t.Fatalf("actions = %+v, want a QR payload and an image url", resp.Actions)
	}
	if resp.Actions[0].Type != provider.ActionQRCode || resp.Actions[1].Type != provider.ActionURL {
		t.Errorf("action types = %q, %q", resp.Actions[0].Type, resp.Actions[1].Type)
	}
}

func TestCreateEWallet(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathDebitPayment, `{
		"responseCode": "2005400",
		"responseMessage": "Success",
		"partnerReferenceNo": "000000000056",
		"appRedirectUrl": "ovo://pay/abc",
		"webRedirectUrl": "https://pay.uat.airpay.co.id/h5pay/pay?token=xyz",
		"additionalInfo": {
			"contractId": "so4c0a61fa-5ad4-4fee-99ed-2a7b06b30c4b",
			"expiredTime": "2023-09-05T17:10:11+07:00",
			"channel": "OVO",
			"instructions": []
		}
	}`)

	p := newTestProvider(t, g, provider.Config{"callback_url": "https://openpay.example/webhooks/winpay"})
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "000000000056",
		Amount:      10000,
		Method:      provider.MethodEWallet,
		Channel:     "ovo",
		Customer:    provider.Customer{Name: "Chus Pandi", Phone: "0895633156874"},
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.ProviderReference != "so4c0a61fa-5ad4-4fee-99ed-2a7b06b30c4b" {
		t.Errorf("provider reference = %q", resp.ProviderReference)
	}
	if resp.ExpiresAt == nil {
		t.Error("expected expiry from additionalInfo.expiredTime")
	}
	if len(resp.Actions) != 2 {
		t.Fatalf("actions = %+v, want a deeplink and a url", resp.Actions)
	}
	if resp.Actions[0].Type != provider.ActionDeeplink || resp.Actions[1].Type != provider.ActionURL {
		t.Errorf("action types = %q, %q", resp.Actions[0].Type, resp.Actions[1].Type)
	}

	sent := g.request(t, http.MethodPost, pathDebitPayment)
	additional, _ := sent["additionalInfo"].(map[string]any)
	if additional["channel"] != "OVO" {
		t.Errorf("channel sent = %v, want OVO", additional["channel"])
	}
	urls, _ := sent["urlParam"].([]any)
	if len(urls) != 2 {
		t.Fatalf("urlParam = %v, want two entries", sent["urlParam"])
	}
	notify, _ := urls[0].(map[string]any)
	if notify["type"] != "PAY_NOTIFY" || notify["url"] != "https://openpay.example/webhooks/winpay" {
		t.Errorf("PAY_NOTIFY entry = %v", notify)
	}
}

func TestCreateEWalletRequiresPhone(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, provider.Config{"callback_url": "https://openpay.example/webhooks/winpay"})

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "ref-1", Amount: 10000, Method: provider.MethodEWallet, Channel: "OVO",
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCreateUnsupportedMethod(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "ref-1", Amount: 10000, Method: provider.MethodCard,
	})
	if !errors.Is(err, provider.ErrUnsupportedOperation) {
		t.Fatalf("expected ErrUnsupportedOperation, got %v", err)
	}
}

func TestCreateRejectsNonIDR(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "ref-1", Amount: 10000, Method: provider.MethodQRIS, Currency: "USD",
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestCreateSurfacesGatewayError(t *testing.T) {
	g := newTestGateway(t)
	g.on(pathCreateVA, `{"responseCode":"4092701","responseMessage":"Duplicate trxId"}`)

	p := newTestProvider(t, g, nil)
	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "INV-0001", Amount: 25000, Method: provider.MethodVirtualAccount, Channel: "BCA",
	})
	if !errors.Is(err, provider.ErrDuplicateReference) {
		t.Fatalf("expected ErrDuplicateReference, got %v", err)
	}
	var perr *provider.Error
	if !errors.As(err, &perr) || perr.Code != "4092701" {
		t.Errorf("expected the gateway code to be preserved, got %#v", err)
	}
}

// --- Inquiry ---------------------------------------------------------------

func createVirtualAccount(t *testing.T, g *testGateway, p *Provider, reference string) {
	t.Helper()
	g.on(pathCreateVA, `{
		"responseCode": "2002700",
		"responseMessage": "Success",
		"virtualAccountData": {
			"virtualAccountNo": "   2269141693898987",
			"trxId": "`+reference+`",
			"totalAmount": {"value": "25000.00", "currency": "IDR"},
			"expiredDate": "2023-09-05T19:30:14+07:00",
			"additionalInfo": {"channel": "BCA", "contractId": "ci-1"}
		}
	}`)
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: reference, Amount: 25000, Method: provider.MethodVirtualAccount, Channel: "BCA",
	}); err != nil {
		t.Fatalf("seeding payment: %v", err)
	}
}

func TestInquiryVirtualAccount(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	createVirtualAccount(t, g, p, "INV-0001")

	g.on(pathStatusVA, `{
		"responseCode": "2002600",
		"responseMessage": "Successful",
		"virtualAccountData": {
			"virtualAccountNo": "2269141708949044",
			"virtualAccountName": "Valid 14",
			"paymentFlagStatus": "00",
			"transactionDate": "2024-02-26T19:04:51+00:00",
			"referenceNo": "50966",
			"totalAmount": {"value": "25000.00", "currency": "IDR"}
		}
	}`)

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

	sent := g.request(t, http.MethodPost, pathStatusVA)
	if sent["virtualAccountNo"] != "2269141693898987" {
		t.Errorf("virtualAccountNo = %v", sent["virtualAccountNo"])
	}
	additional, _ := sent["additionalInfo"].(map[string]any)
	if additional["contractId"] != "ci-1" {
		t.Errorf("contractId = %v, want ci-1", additional["contractId"])
	}
}

func TestInquiryQRIS(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	g.on(pathQRISGenerate, `{
		"responseCode": "2004700", "responseMessage": "Success", "partnerReferenceNo": "ref-1",
		"qrUrl": "https://example/qr", "additionalInfo": {"contractId": "qr-1", "expiredAt": "2023-09-06T23:08:56+07:00"}
	}`)
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "ref-1", Amount: 45000, Method: provider.MethodQRIS,
	}); err != nil {
		t.Fatalf("seeding payment: %v", err)
	}

	g.on(pathQRISQuery, `{
		"responseCode": "2005100", "responseMessage": "Success",
		"originalPartnerReferenceNo": "ref-1", "serviceCode": "47",
		"latestTransactionStatus": "00", "transactionStatusDesc": "Success",
		"paidTime": "2023-08-24T17:03:33+07:00",
		"amount": {"value": "45000.00", "currency": "IDR"}
	}`)

	resp, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "ref-1"})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if resp.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid", resp.Status)
	}
	if resp.Amount != 45000 {
		t.Errorf("amount = %d, want 45000", resp.Amount)
	}
}

func TestInquiryUnknownReference(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "nope"})
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- Cancel ----------------------------------------------------------------

func TestCancelVirtualAccount(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	createVirtualAccount(t, g, p, "INV-0001")

	g.on(pathDeleteVA, `{
		"responseCode": "2003100", "responseMessage": "Success",
		"virtualAccountData": {"trxId": "INV-0001"},
		"additionalInfo": {"contractId": "ci-1", "channel": "BCA"}
	}`)

	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "INV-0001", Reason: "expire"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusCancelled {
		t.Errorf("status = %q, want cancelled", resp.Status)
	}

	sent := g.request(t, http.MethodDelete, pathDeleteVA)
	if sent["virtualAccountNo"] != "2269141693898987" {
		t.Errorf("virtualAccountNo = %v", sent["virtualAccountNo"])
	}
}

func TestCancelQRIS(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	g.on(pathQRISGenerate, `{
		"responseCode": "2004700", "responseMessage": "Success", "partnerReferenceNo": "ref-1",
		"qrUrl": "https://example/qr", "additionalInfo": {"contractId": "qr-1", "expiredAt": "2023-09-06T23:08:56+07:00"}
	}`)
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "ref-1", Amount: 45000, Method: provider.MethodQRIS,
	}); err != nil {
		t.Fatalf("seeding payment: %v", err)
	}

	g.on(pathQRISCancel, `{
		"responseCode": "2007700", "responseMessage": "Request has been processed successfully",
		"originalPartnerReferenceNo": "ref-1", "cancelTime": "2024-05-30T18:08:05+07:00",
		"additionalInfo": {"contractId": "qr-1"}
	}`)

	resp, err := p.Cancel(context.Background(), &provider.CancelRequest{ReferenceID: "ref-1", Reason: "Gagal"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if resp.Status != provider.StatusCancelled {
		t.Errorf("status = %q, want cancelled", resp.Status)
	}

	sent := g.request(t, http.MethodPost, pathQRISCancel)
	if sent["reason"] != "Gagal" {
		t.Errorf("reason = %v, want Gagal", sent["reason"])
	}
}

// --- ParseWebhook ----------------------------------------------------------

func TestParseWebhookVirtualAccount(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	// referenceNo is a bare number in the documented payload.
	body := []byte(`{
		"partnerServiceId": "   22691",
		"customerNo": "41693903614",
		"virtualAccountNo": "   2269141693903614",
		"virtualAccountName": "Bayar 2269141693903614",
		"trxId": "INV-0001",
		"paymentRequestId": "88889123",
		"paidAmount": {"value": "10000", "currency": "IDR"},
		"trxDateTime": "2023-09-05T22:47:00+07:00",
		"referenceNo": 36238,
		"additionalInfo": {"channel": "CIMB", "contractId": "ci71a51730-2373-455f-b538-3f9912fefb73"}
	}`)

	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body, Path: "/webhooks/winpay"})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventVAPayment {
		t.Errorf("event = %q", evt.EventType)
	}
	if evt.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid", evt.Status)
	}
	if evt.ReferenceID != "INV-0001" {
		t.Errorf("reference = %q", evt.ReferenceID)
	}
	if evt.ProviderReference != "ci71a51730-2373-455f-b538-3f9912fefb73" {
		t.Errorf("provider reference = %q", evt.ProviderReference)
	}
	if evt.Amount != 10000 {
		t.Errorf("amount = %d, want 10000", evt.Amount)
	}
	if evt.PaidAt == nil {
		t.Error("expected paid_at")
	}
}

func TestParseWebhookQRIS(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	body := []byte(`{
		"originalReferenceNo": "45584",
		"originalPartnerReferenceNo": "ref-1",
		"latestTransactionStatus": "00",
		"amount": {"value": "45000", "currency": "IDR"},
		"additionalInfo": {
			"channel": "QRIS", "contractId": "qr748ad360-9755-40af-a201-e44b45c4568f",
			"brandName": "DANA", "rrn": "027364710019", "buyerRef": "WINPAY", "terminalId": null
		}
	}`)

	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventQRISPayment {
		t.Errorf("event = %q, want %q", evt.EventType, EventQRISPayment)
	}
	if evt.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid", evt.Status)
	}
	if evt.ReferenceID != "ref-1" {
		t.Errorf("reference = %q", evt.ReferenceID)
	}
	if evt.Amount != 45000 {
		t.Errorf("amount = %d, want 45000", evt.Amount)
	}
}

func TestParseWebhookEWallet(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	body := []byte(`{
		"originalPartnerReferenceNo": "ref-1",
		"originalReferenceNo": "45588",
		"merchantId": 170041,
		"amount": {"value": "1000", "currency": "IDR"},
		"latestTransactionStatus": "07",
		"additionalInfo": {"channel": "OVO", "contractId": "ovb7b5599b-cce7-4f0f-8c34-fa283185945b"}
	}`)

	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventEWalletPayment {
		t.Errorf("event = %q, want %q", evt.EventType, EventEWalletPayment)
	}
	// "07" means "unpaid" for eWallets, not "not found" as it does for QRIS.
	if evt.Status != provider.StatusPending {
		t.Errorf("status = %q, want pending", evt.Status)
	}
}

func TestCreateModernRetailPayment(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	g.on(pathCreateVA, `{
		"responseCode": "2002700", "responseMessage": "Success",
		"virtualAccountData": {
			"virtualAccountNo": "99887766", "trxId": "retail-1",
			"totalAmount": {"value": "25000.00", "currency": "IDR"},
			"expiredDate": "2024-01-11T10:00:00+07:00",
			"additionalInfo": {"channel": "INDOMARET", "contractId": "retail-contract-1"}
		}
	}`)

	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "retail-1", Amount: 25000, Method: provider.MethodRetailOutlet, Channel: "INDOMARET",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.ProviderReference != "retail-contract-1" || resp.PaymentCode != "99887766" {
		t.Fatalf("response = %+v, want retail contract and payment code", resp)
	}
}

func TestParseWebhookModernRetail(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)
	body := []byte(`{
		"partnerServiceId": "   22691", "customerNo": "41693903614",
		"virtualAccountNo": "   2269141693903614", "virtualAccountName": "Bayar",
		"trxId": "retail-1", "paymentRequestId": "88889123",
		"paidAmount": {"value": "10000", "currency": "IDR"},
		"trxDateTime": "2024-01-11T10:00:00+07:00", "referenceNo": 36238,
		"additionalInfo": {"channel": "INDOMARET", "contractId": "retail-contract-1"}
	}`)

	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.EventType != EventVAPayment || evt.Status != provider.StatusPaid {
		t.Fatalf("event = %q status = %q, want VA payment/paid", evt.EventType, evt.Status)
	}
	if evt.ReferenceID != "retail-1" || evt.ProviderReference != "retail-contract-1" {
		t.Fatalf("event references = %+v", evt)
	}
}

func TestParseWebhookEnrichesReferenceFromCache(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	g.on(pathQRISGenerate, `{
		"responseCode": "2004700", "responseMessage": "Success", "partnerReferenceNo": "ref-1",
		"qrUrl": "https://example/qr", "additionalInfo": {"contractId": "qr-1", "expiredAt": "2023-09-06T23:08:56+07:00"}
	}`)
	if _, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "ref-1", Amount: 45000, Method: provider.MethodQRIS,
	}); err != nil {
		t.Fatalf("seeding payment: %v", err)
	}

	// Callback without the merchant reference, only the contractId.
	body := []byte(`{"latestTransactionStatus":"00","amount":{"value":"45000","currency":"IDR"},
		"additionalInfo":{"channel":"QRIS","contractId":"qr-1"}}`)
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.ReferenceID != "ref-1" {
		t.Errorf("reference = %q, want the cached ref-1", evt.ReferenceID)
	}
}

func TestParseWebhookUnrecognized(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	_, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: []byte(`{"hello":"world"}`)})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

// --- VerifyWebhookSignature ------------------------------------------------

func TestVerifyWebhookSignature(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, provider.Config{"public_key": publicKeyPEM(t, &g.key.PublicKey)})

	body := []byte(`{"latestTransactionStatus":"00","amount":{"value":"1000","currency":"IDR"},"additionalInfo":{"channel":"OVO","contractId":"ov-1"}}`)
	compact, err := minify(body)
	if err != nil {
		t.Fatalf("minify: %v", err)
	}
	timestamp := "2024-01-11T17:01:35+07:00"
	signature, err := signRequest(g.key, http.MethodPost, "/webhooks/winpay", compact, timestamp)
	if err != nil {
		t.Fatalf("signRequest: %v", err)
	}

	valid := &provider.WebhookRequest{
		Body:    compact,
		Path:    "/webhooks/winpay",
		Headers: signatureHeaders(timestamp, signature),
	}
	if err := p.VerifyWebhookSignature(valid); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}

	tampered := &provider.WebhookRequest{
		Body:    []byte(`{"latestTransactionStatus":"00","amount":{"value":"9999","currency":"IDR"},"additionalInfo":{"channel":"OVO","contractId":"ov-1"}}`),
		Path:    "/webhooks/winpay",
		Headers: signatureHeaders(timestamp, signature),
	}
	if err := p.VerifyWebhookSignature(tampered); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for a tampered body, got %v", err)
	}

	missing := &provider.WebhookRequest{Body: compact, Path: "/webhooks/winpay"}
	if err := p.VerifyWebhookSignature(missing); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for missing headers, got %v", err)
	}
}

func TestVerifyWebhookSignatureUsesConfiguredPath(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, provider.Config{
		"public_key":    publicKeyPEM(t, &g.key.PublicKey),
		"callback_path": "/v1.0/transfer-va/payment",
	})

	body := []byte(`{"paidAmount":{"value":"10000","currency":"IDR"},"trxId":"INV-1"}`)
	timestamp := "2024-01-11T08:57:55+07:00"
	signature, err := signRequest(g.key, http.MethodPost, "/v1.0/transfer-va/payment", body, timestamp)
	if err != nil {
		t.Fatalf("signRequest: %v", err)
	}

	// The path on the request differs; the configured one must win.
	req := &provider.WebhookRequest{
		Body:    body,
		Path:    "/webhooks/winpay",
		Headers: signatureHeaders(timestamp, signature),
	}
	if err := p.VerifyWebhookSignature(req); err != nil {
		t.Errorf("configured callback_path was not honoured: %v", err)
	}
}

func TestVerifyWebhookSignatureUsesDocumentedPathForCommonRoute(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, provider.Config{"public_key": publicKeyPEM(t, &g.key.PublicKey)})
	body := []byte(`{"paidAmount":{"value":"10000","currency":"IDR"},"trxId":"retail-1","virtualAccountNo":"99887766","additionalInfo":{"channel":"INDOMARET","contractId":"retail-contract-1"}}`)
	timestamp := "2024-01-11T08:57:55+07:00"
	signature, err := signRequest(g.key, http.MethodPost, "/v1.0/transfer-va/payment", body, timestamp)
	if err != nil {
		t.Fatalf("signRequest: %v", err)
	}

	req := &provider.WebhookRequest{
		Body:    body,
		Path:    "/webhooks/winpay",
		Headers: signatureHeaders(timestamp, signature),
	}
	if err := p.VerifyWebhookSignature(req); err != nil {
		t.Fatalf("documented callback path rejected: %v", err)
	}
}

func TestVerifyWebhookSignatureSkippedWithoutPublicKey(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if err := p.VerifyWebhookSignature(&provider.WebhookRequest{Body: []byte(`{}`)}); err != nil {
		t.Errorf("expected verification to be skipped when no public key is configured, got %v", err)
	}
}

func TestWebhookResponse(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	cases := map[string]string{
		EventVAPayment:      responseVAPayment,
		EventQRISPayment:    responseQRISPayment,
		EventEWalletPayment: responseEWalletPayment,
	}
	for event, want := range cases {
		status, body := p.WebhookResponse(&provider.WebhookEvent{EventType: event})
		if status != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", event, status)
		}
		if !strings.Contains(string(body), want) {
			t.Errorf("%s: body = %s, want responseCode %s", event, body, want)
		}
	}
}

// --- mapping and config ----------------------------------------------------

func TestMapTransactionStatus(t *testing.T) {
	cases := []struct {
		code     string
		notFound provider.Status
		want     provider.Status
	}{
		{"00", provider.StatusUnknown, provider.StatusPaid},
		{"01", provider.StatusUnknown, provider.StatusPending},
		{"02", provider.StatusUnknown, provider.StatusPending},
		{"03", provider.StatusUnknown, provider.StatusPending},
		{"04", provider.StatusUnknown, provider.StatusRefunded},
		{"05", provider.StatusUnknown, provider.StatusCancelled},
		{"06", provider.StatusUnknown, provider.StatusFailed},
		{"07", provider.StatusUnknown, provider.StatusUnknown},
		{"07", provider.StatusPending, provider.StatusPending},
		{"99", provider.StatusUnknown, provider.StatusUnknown},
	}
	for _, tc := range cases {
		if got := mapTransactionStatus(tc.code, tc.notFound); got != tc.want {
			t.Errorf("mapTransactionStatus(%q, %q) = %q, want %q", tc.code, tc.notFound, got, tc.want)
		}
	}
}

func TestMapVAStatus(t *testing.T) {
	cases := map[string]provider.Status{
		"00": provider.StatusPaid,
		"01": provider.StatusPending,
		"02": provider.StatusPending,
		"":   provider.StatusUnknown,
	}
	for code, want := range cases {
		if got := mapVAStatus(code); got != want {
			t.Errorf("mapVAStatus(%q) = %q, want %q", code, got, want)
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

func TestChannelMethod(t *testing.T) {
	cases := map[string]provider.Method{
		"QRIS":      provider.MethodQRIS,
		"OVO":       provider.MethodEWallet,
		"SPAY":      provider.MethodEWallet,
		"BCA":       provider.MethodVirtualAccount,
		"INDOMARET": provider.MethodRetailOutlet,
		"  bni  ":   provider.MethodVirtualAccount,
		"UNKNOWN":   "",
		"":          "",
	}
	for channel, want := range cases {
		if got := channelMethod(channel); got != want {
			t.Errorf("channelMethod(%q) = %q, want %q", channel, got, want)
		}
	}
}

func TestAccountName(t *testing.T) {
	if got := accountName("Budi", "INV-123"); len(got) < 5 || len(got) > 24 {
		t.Errorf("accountName produced %q (len %d), want 5..24 characters", got, len(got))
	}
	long := strings.Repeat("a", 40)
	if got := accountName(long, "INV-1"); len(got) != 24 {
		t.Errorf("accountName(long) = %q (len %d), want 24", got, len(got))
	}
	if got := accountName("Chus Pandi", "INV-1"); got != "Chus Pandi" {
		t.Errorf("accountName = %q, want unchanged when valid", got)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	key := sharedKey(t)
	valid := provider.Config{"partner_id": "p", "private_key": privateKeyPEM(key)}

	if _, err := New(provider.Config{"private_key": valid["private_key"]}); err == nil {
		t.Error("expected an error when partner_id is missing")
	}
	if _, err := New(provider.Config{"partner_id": "p"}); err == nil {
		t.Error("expected an error when private_key is missing")
	}
	if _, err := New(provider.Config{"partner_id": "p", "private_key": "not-a-key"}); err == nil {
		t.Error("expected an error for an unparseable private key")
	}
	if _, err := New(provider.Config{
		"partner_id": "p", "private_key": valid["private_key"], "timeout": "nope",
	}); err == nil {
		t.Error("expected an error for an invalid timeout")
	}
}

func TestPrivateKeyAcceptsBase64DER(t *testing.T) {
	key := sharedKey(t)
	der := base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(key))

	p, err := New(provider.Config{"partner_id": "p", "private_key": der})
	if err != nil {
		t.Fatalf("New with base64 DER: %v", err)
	}
	if p.privateKey == nil {
		t.Error("expected the private key to be parsed")
	}
}

func TestRegistryFactory(t *testing.T) {
	key := sharedKey(t)
	cfg := provider.Config{"partner_id": "p", "private_key": privateKeyPEM(key)}

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
	for _, method := range []provider.Method{provider.MethodVirtualAccount, provider.MethodRetailOutlet, provider.MethodQRIS, provider.MethodEWallet} {
		if !caps.SupportsMethod(method) {
			t.Errorf("expected %q to be supported", method)
		}
	}
	if !caps.SupportsChannel("BCA") || !caps.SupportsChannel("QRIS") || !caps.SupportsChannel("INDOMARET") {
		t.Error("expected VA, QRIS and modern-retail channels to be advertised")
	}
	// Channel matching must tolerate the case/whitespace callers use.
	if !caps.SupportsChannel("bca") || !caps.SupportsChannel("  ovo  ") {
		t.Error("expected channel matching to be case-insensitive")
	}
	if caps.SupportsChannel("NOPE") {
		t.Error("expected an unknown channel to be rejected")
	}
	if !caps.SupportsChannel("") {
		t.Error("an empty channel must always be accepted")
	}
	if !caps.SupportsInquiry || !caps.SupportsCancel || !caps.SupportsWebhook {
		t.Error("expected inquiry, cancel and webhook support")
	}
}

func TestExpiryClampsToMaximum(t *testing.T) {
	g := newTestGateway(t)
	p := newTestProvider(t, g, nil)

	if d := time.Until(p.expiry(0)); d <= 0 || d > defaultExpiry+time.Minute {
		t.Errorf("default expiry = %v, want ~%v", d, defaultExpiry)
	}
	if d := time.Until(p.expiry(365 * 24 * time.Hour)); d > maxExpiry+time.Minute {
		t.Errorf("expiry = %v, want it clamped to %v", d, maxExpiry)
	}
}
