// Package midtrans implements the [provider.Provider] contract for Midtrans'
// legacy Core API (https://docs.midtrans.com/reference/core-api-overview), the
// REST product built around POST /v2/charge and addressed by order_id.
//
// It is deliberately separate from the sibling package pkg/provider/midtrans-snap,
// which targets Midtrans' BI-SNAP Core API — the Bank Indonesia "Standar
// Nasional Open API Pembayaran" interface. The two are different products with
// different authentication, endpoints and payloads, so they ship as two
// providers:
//
//	midtrans       this package: the legacy Core API (/v2/charge)
//	midtrans-snap  the BI-SNAP Core API (/v1.0/...)
//
// Neither implements the hosted Snap checkout product. This adapter covers:
//
//   - Virtual Account — bank_transfer for Permata, BCA, BNI, BRI, CIMB, Danamon,
//     BSI, SeaBank and Saqu, plus Mandiri Bill Payment (echannel).
//   - QRIS — payment_type "qris".
//   - eWallet — GoPay and ShopeePay.
//   - Retail outlet — Indomaret and Alfamart (payment_type "cstore").
//
// # Authentication
//
// Every request uses HTTP Basic Auth with the Midtrans server key as the
// username and an empty password:
//
//	Authorization: Basic base64("<server_key>:")
//
// # Callbacks
//
// Midtrans posts a JSON notification to the merchant notification URL and
// authenticates it with a signature_key field:
//
//	SHA512(order_id + status_code + gross_amount + server_key)
//
// hex encoded. The adapter recomputes it in constant time and refuses callbacks
// when server_key is not configured. Midtrans acknowledges a notification on
// any 2xx response, so no special acknowledgement body is required.
//
// # Configuration
//
// Keys are read from OPENPAY_PROVIDER_MIDTRANS_<KEY>:
//
//	server_key                Midtrans server key (required): the Basic-auth
//	                          secret and the notification signature key
//	base_url                  https://api.sandbox.midtrans.com (default) or
//	                          https://api.midtrans.com for production
//	qris_acquirer             QRIS acquirer (default "gopay")
//	gopay_callback_url        optional callback_url sent with GoPay charges
//	shopee_pay_callback_url   optional callback_url sent with ShopeePay charges
//	default_expiry            payment lifetime as a Go duration (default "24h")
//	timeout                   per-request HTTP timeout (default "30s")
//
// # Per-payment overrides
//
// Two gateway values travel through the unified [provider.CreatePaymentRequest]
// because they are not part of the gateway-agnostic contract:
//
//	va_number       virtual account only: a customer-chosen VA number where the
//	                bank supports one
//	return_url      e-wallet only: the URL the payer returns to after completing
//	                the payment; overrides the configured callback_url
//	qris_acquirer   QRIS only: acquirer override for a single payment
//
// # Order id mapping
//
// The Core API status, cancel and expire endpoints are addressed by the
// order_id (or transaction_id) in the path. This adapter passes the merchant
// ReferenceID through verbatim as order_id, so — unlike the BI-SNAP adapter —
// no in-process correlation cache is needed: Inquiry and Cancel resolve from
// the reference alone.
//
// # Sandbox status
//
// Only the sandbox base URL is the default. Channel availability is
// account-specific and has NOT been verified against the live Midtrans sandbox.
package midtrans

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// Name is the provider identifier used in configuration, routing and callback
// URLs ("/webhooks/midtrans"). It denotes the legacy Core API; the BI-SNAP
// adapter is registered as "midtrans-snap".
const Name = "midtrans"

// Operation names used in [provider.Error].
const (
	opCreate  = "CreatePayment"
	opInquiry = "Inquiry"
	opCancel  = "Cancel"
	opWebhook = "ParseWebhook"
	opVerify  = "VerifyWebhookSignature"
)

// HTTP header names.
const headerAuthorization = "Authorization"

// Legacy Core API endpoint paths, relative to the base URL.
const pathCharge = "/v2/charge"

// Defaults.
const (
	defaultBaseURL   = "https://api.sandbox.midtrans.com"
	defaultTimeout   = 30 * time.Second
	defaultExpiry    = 24 * time.Hour
	maxResponseBytes = 1 << 20 // 1 MiB
)

// Provider implements [provider.Provider] for Midtrans' legacy Core API. It is
// safe for concurrent use.
type Provider struct {
	serverKey string
	baseURL   string

	qrisAcquirer         string
	gopayCallbackURL     string
	shopeepayCallbackURL string
	defaultExpiry        time.Duration
	client               *http.Client
}

// New builds a Midtrans Core API adapter from configuration. See the package
// documentation for the recognized keys.
func New(cfg provider.Config) (*Provider, error) {
	serverKey, err := cfg.Required("server_key")
	if err != nil {
		return nil, err
	}

	p := &Provider{
		serverKey:            serverKey,
		baseURL:              strings.TrimRight(cfg.Get("base_url", defaultBaseURL), "/"),
		qrisAcquirer:         strings.ToLower(strings.TrimSpace(cfg.Get("qris_acquirer", qrisDefaultAcquirer))),
		gopayCallbackURL:     strings.TrimSpace(cfg.Get("gopay_callback_url", "")),
		shopeepayCallbackURL: strings.TrimSpace(cfg.Get("shopee_pay_callback_url", "")),
		defaultExpiry:        defaultExpiry,
		client:               provider.NewHTTPClient(Name, defaultTimeout),
	}
	if p.qrisAcquirer == "" {
		p.qrisAcquirer = qrisDefaultAcquirer
	}
	if raw := cfg.Get("timeout", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("midtrans: invalid timeout %q: %w", raw, err)
		}
		p.client.Timeout = d
	}
	if raw := cfg.Get("default_expiry", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("midtrans: invalid default_expiry %q: %w", raw, err)
		}
		p.defaultExpiry = d
	}
	return p, nil
}

// init registers the factory so the host application can build the Core API
// adapter from configuration alone.
func init() {
	provider.Register(Name, func(cfg provider.Config) (provider.Provider, error) {
		return New(cfg)
	})
}

// Name implements [provider.Provider].
func (p *Provider) Name() string { return Name }

// Capabilities implements [provider.Provider].
func (p *Provider) Capabilities() provider.Capabilities {
	channels := make([]string, 0, len(vaBanks)+len(ewalletChannels)+len(retailChannels)+2)
	for code := range vaBanks {
		channels = append(channels, code)
	}
	channels = append(channels, MandiriChannel, "QRIS")
	for code := range ewalletChannels {
		channels = append(channels, code)
	}
	for code := range retailChannels {
		channels = append(channels, code)
	}
	sort.Strings(channels)

	return provider.Capabilities{
		Methods: []provider.Method{
			provider.MethodVirtualAccount,
			provider.MethodQRIS,
			provider.MethodEWallet,
			provider.MethodRetailOutlet,
		},
		Channels:        channels,
		SupportsInquiry: true,
		SupportsCancel:  true,
		SupportsWebhook: true,
		SupportsSandbox: true,
	}
}

// Health implements [provider.HealthChecker].
func (p *Provider) Health(context.Context) error { return nil }

// --- payments --------------------------------------------------------------

// CreatePayment implements [provider.Provider].
func (p *Provider) CreatePayment(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opCreate, err)
	}
	if req == nil {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST", "nil request", provider.ErrInvalidRequest)
	}
	if strings.TrimSpace(req.ReferenceID) == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST", "reference_id is required", provider.ErrInvalidRequest)
	}
	if req.Amount <= 0 {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST", "amount must be positive", provider.ErrInvalidRequest)
	}
	if req.Currency != "" && req.Currency != provider.IDR {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("midtrans only settles IDR, got %q", req.Currency), provider.ErrInvalidRequest)
	}

	switch req.Method {
	case provider.MethodVirtualAccount:
		return p.createVA(ctx, req)
	case provider.MethodQRIS:
		return p.createQRIS(ctx, req)
	case provider.MethodEWallet:
		return p.createEWallet(ctx, req)
	case provider.MethodRetailOutlet:
		return p.createCStore(ctx, req)
	default:
		return nil, provider.NewError(Name, opCreate, "UNSUPPORTED_METHOD",
			fmt.Sprintf("midtrans Core API supports virtual account, QRIS, e-wallet and retail outlet, not %q", req.Method),
			provider.ErrUnsupportedOperation)
	}
}

func (p *Provider) createVA(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	if channel == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"virtual accounts require a channel code (e.g. BCA, BNI, MANDIRI)", provider.ErrInvalidRequest)
	}

	payload := p.baseCharge(req)
	switch channel {
	case MandiriChannel:
		payload.PaymentType = "echannel"
		payload.ECHannel = &echannel{BillInfo1: "Payment", BillInfo2: descriptionOr(req)}
	default:
		bank, ok := vaBanks[channel]
		if !ok {
			return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
				fmt.Sprintf("unsupported virtual account channel %q", channel), provider.ErrInvalidRequest)
		}
		payload.PaymentType = "bank_transfer"
		payload.BankTransfer = &bankTransfer{Bank: bank}
		if vaNo := strings.TrimSpace(req.Metadata["va_number"]); vaNo != "" {
			payload.BankTransfer.VANumber = vaNo
		}
	}

	raw, out, err := p.charge(ctx, payload, opCreate)
	if err != nil {
		return nil, err
	}
	return createResponse(req, out, raw), nil
}

func (p *Provider) createQRIS(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	if channel := strings.ToUpper(strings.TrimSpace(req.Channel)); channel != "" && channel != "QRIS" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("qris channel must be %q, got %q", "QRIS", req.Channel), provider.ErrInvalidRequest)
	}
	acquirer := firstNonEmpty(req.Metadata["qris_acquirer"], p.qrisAcquirer)

	payload := p.baseCharge(req)
	payload.PaymentType = "qris"
	payload.QRIS = &qris{Acquirer: strings.ToLower(acquirer)}

	raw, out, err := p.charge(ctx, payload, opCreate)
	if err != nil {
		return nil, err
	}
	return createResponse(req, out, raw), nil
}

func (p *Provider) createEWallet(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	if channel == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"e-wallets require a channel code (GOPAY, SHOPEEPAY)", provider.ErrInvalidRequest)
	}
	paymentType, ok := ewalletChannels[channel]
	if !ok {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("unsupported e-wallet channel %q (GOPAY, SHOPEEPAY)", req.Channel), provider.ErrInvalidRequest)
	}

	// The e-wallet return URL is where the payer is redirected after completing
	// payment, so it must come from the payment request (or per-provider
	// config), not from the webhook configuration.
	returnURL := firstNonEmpty(req.Metadata["return_url"], req.CallbackURL)

	payload := p.baseCharge(req)
	payload.PaymentType = paymentType
	switch paymentType {
	case "gopay":
		callback := firstNonEmpty(returnURL, p.gopayCallbackURL)
		payload.GoPay = &gopay{EnableCallback: callback != "", CallbackURL: callback}
	case "shopeepay":
		payload.ShopeePay = &shopeePay{CallbackURL: firstNonEmpty(returnURL, p.shopeepayCallbackURL)}
	}

	raw, out, err := p.charge(ctx, payload, opCreate)
	if err != nil {
		return nil, err
	}
	return createResponse(req, out, raw), nil
}

func (p *Provider) createCStore(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	store, ok := retailChannels[channel]
	if !ok {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("unsupported retail outlet channel %q (INDOMARET, ALFAMART)", req.Channel), provider.ErrInvalidRequest)
	}

	payload := p.baseCharge(req)
	payload.PaymentType = "cstore"
	payload.CStore = &cstore{Store: store, Message: descriptionOr(req)}

	raw, out, err := p.charge(ctx, payload, opCreate)
	if err != nil {
		return nil, err
	}
	return createResponse(req, out, raw), nil
}

// baseCharge assembles the payment-type-independent part of a charge request.
func (p *Provider) baseCharge(req *provider.CreatePaymentRequest) *chargeRequest {
	return &chargeRequest{
		TransactionDetails: transactionDetails{
			OrderID:     strings.TrimSpace(req.ReferenceID),
			GrossAmount: req.Amount,
		},
		CustomerDetails: customerDetailsFrom(req.Customer),
		ItemDetails:     itemDetailsFrom(req.Items),
		CustomExpiry:    customExpiryOf(p.expiry(req.Expiry)),
	}
}

// charge posts a charge request, decodes the response and validates the
// response-level status_code.
func (p *Provider) charge(ctx context.Context, payload *chargeRequest, op string) (json.RawMessage, *chargeResponse, error) {
	raw, err := p.call(ctx, http.MethodPost, pathCharge, payload, op)
	if err != nil {
		return nil, nil, err
	}
	var out chargeResponse
	if err := decode(raw, &out); err != nil {
		return nil, nil, provider.WrapError(Name, op, err)
	}
	if err := checkStatus(op, out.apiResponse); err != nil {
		return nil, nil, err
	}
	return raw, &out, nil
}

// createResponse maps a successful charge onto the unified create response.
func createResponse(req *provider.CreatePaymentRequest, out *chargeResponse, raw json.RawMessage) *provider.CreatePaymentResponse {
	code, actions := paymentDetails(out)
	return &provider.CreatePaymentResponse{
		ProviderReference: firstNonEmpty(out.TransactionID, out.OrderID, req.ReferenceID),
		ReferenceID:       firstNonEmpty(out.OrderID, req.ReferenceID),
		Status:            createStatus(out),
		Amount:            amountOr(out.GrossAmount, req.Amount),
		PaymentCode:       code,
		Actions:           actions,
		ExpiresAt:         parseTime(out.ExpiryTime),
		Raw:               raw,
	}
}

// createStatus maps the transaction_status of a freshly created charge. A
// successful charge is always at least pending.
func createStatus(out *chargeResponse) provider.Status {
	if status := mapTransactionStatus(out.TransactionStatus, out.FraudStatus); status != provider.StatusUnknown {
		return status
	}
	return provider.StatusPending
}

// --- inquiry ---------------------------------------------------------------

// Inquiry implements [provider.Provider]. The Core API status endpoint is
// addressed by order_id (or transaction_id) in the path, so no correlation
// cache is required.
func (p *Provider) Inquiry(ctx context.Context, req *provider.InquiryRequest) (*provider.InquiryResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if req == nil || (strings.TrimSpace(req.ReferenceID) == "" && strings.TrimSpace(req.ProviderReference) == "") {
		return nil, provider.NewError(Name, opInquiry, "INVALID_REQUEST",
			"reference_id or provider_reference is required", provider.ErrInvalidRequest)
	}

	id := addressID(req.ReferenceID, req.ProviderReference)
	raw, err := p.call(ctx, http.MethodGet, statusPath(id), nil, opInquiry)
	if err != nil {
		return nil, err
	}
	var out chargeResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if err := checkStatus(opInquiry, out.apiResponse); err != nil {
		return nil, err
	}

	status := mapTransactionStatus(out.TransactionStatus, out.FraudStatus)
	code, _ := paymentDetails(&out)
	return &provider.InquiryResponse{
		ReferenceID:       firstNonEmpty(out.OrderID, req.ReferenceID),
		ProviderReference: firstNonEmpty(out.TransactionID, req.ProviderReference),
		Status:            status,
		Amount:            amountOr(out.GrossAmount, 0),
		PaymentCode:       code,
		PaidAt:            paidAtIfPaid(status, parseTime(firstNonEmpty(out.SettlementTime, out.TransactionTime))),
		ExpiresAt:         parseTime(out.ExpiryTime),
		Raw:               raw,
	}, nil
}

// --- cancel ----------------------------------------------------------------

// Cancel implements [provider.Provider]. The Core API exposes two distinct
// operations: /cancel voids a pending transaction and /expire force-expires it.
// A Reason of "expire" (case-insensitively) selects the latter.
func (p *Provider) Cancel(ctx context.Context, req *provider.CancelRequest) (*provider.CancelResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}
	if req == nil || (strings.TrimSpace(req.ReferenceID) == "" && strings.TrimSpace(req.ProviderReference) == "") {
		return nil, provider.NewError(Name, opCancel, "INVALID_REQUEST",
			"reference_id or provider_reference is required", provider.ErrInvalidRequest)
	}

	id := addressID(req.ReferenceID, req.ProviderReference)
	path, fallback := cancelPath(id), provider.StatusCancelled
	if strings.EqualFold(strings.TrimSpace(req.Reason), "expire") {
		path, fallback = expirePath(id), provider.StatusExpired
	}

	raw, err := p.call(ctx, http.MethodPost, path, nil, opCancel)
	if err != nil {
		return nil, err
	}
	var out chargeResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}
	if err := checkStatus(opCancel, out.apiResponse); err != nil {
		return nil, err
	}

	status := mapTransactionStatus(out.TransactionStatus, out.FraudStatus)
	if status == provider.StatusUnknown {
		status = fallback
	}
	return &provider.CancelResponse{
		ReferenceID:       firstNonEmpty(out.OrderID, req.ReferenceID, req.ProviderReference),
		ProviderReference: firstNonEmpty(out.TransactionID, req.ProviderReference),
		Status:            status,
		Raw:               raw,
	}, nil
}

// statusPath, cancelPath and expirePath build the order_id-addressed Core API
// paths.
func statusPath(id string) string { return "/v2/" + url.PathEscape(id) + "/status" }
func cancelPath(id string) string { return "/v2/" + url.PathEscape(id) + "/cancel" }
func expirePath(id string) string { return "/v2/" + url.PathEscape(id) + "/expire" }

// --- HTTP ------------------------------------------------------------------

// call sends one Core API request (Basic Auth with the server key) and returns
// the raw response body. Transport-level and HTTP-error responses are mapped
// onto contract sentinels; a 2xx response is returned as-is so the caller can
// validate the response-level status_code via [checkStatus].
func (p *Provider) call(ctx context.Context, method, path string, payload any, op string) (json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, provider.NewError(Name, op, "ENCODE", err.Error(), provider.ErrInvalidRequest)
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, body)
	if err != nil {
		return nil, provider.NewError(Name, op, "REQUEST", err.Error(), provider.ErrGateway)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(headerAuthorization, "Basic "+base64.StdEncoding.EncodeToString([]byte(p.serverKey+":")))

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, provider.NewError(Name, op, "NETWORK", err.Error(), provider.ErrGateway)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, provider.NewError(Name, op, "READ", err.Error(), provider.ErrGateway)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(op, resp.StatusCode, raw)
	}
	return raw, nil
}

// apiError turns an unsuccessful Midtrans response into a [*provider.Error],
// keeping the gateway's status_code and message.
func apiError(op string, httpStatus int, raw []byte) error {
	var envelope apiResponse
	_ = json.Unmarshal(raw, &envelope)

	code := firstNonEmpty(envelope.StatusCode, strconv.Itoa(httpStatus))
	message := firstNonEmpty(envelope.StatusMessage, firstValidationMessage(envelope.ValidationMessages), truncate(string(raw)))
	return &provider.Error{
		Provider:   Name,
		Op:         op,
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
		Err:        classifyStatus(httpStatus, envelope.StatusCode),
	}
}

// decode unmarshals a response body into out, wrapping JSON problems as request
// errors.
func decode(raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: cannot decode midtrans response: %v", provider.ErrInvalidRequest, err)
	}
	return nil
}

// expiry returns the requested lifetime, or the configured default, clamped to
// Midtrans' 180-day maximum.
func (p *Provider) expiry(requested time.Duration) time.Duration {
	d := requested
	if d <= 0 {
		d = p.defaultExpiry
	}
	if d > maxExpiry {
		d = maxExpiry
	}
	return d
}
