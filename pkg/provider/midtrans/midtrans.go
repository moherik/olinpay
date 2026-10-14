// Package midtrans implements the [provider.Provider] contract for Midtrans
// (midtrans.com), one of Indonesia's largest payment gateways.
//
// It uses Midtrans' Core API (https://docs.midtrans.com/reference/charge-transactions-1),
// which charges a specific instrument directly instead of hosting a checkout
// page (that is the Snap API, a different product). It covers:
//
//   - Virtual Account — bank_transfer for BCA, BNI, BRI, PERMATA and CIMB, and
//     the echannel Mandiri Bill Payment product (channel MANDIRI).
//   - QRIS — the dynamic merchant-presented QR (payment_type "qris"); the QR is
//     exposed both as a payment action and as the payment code.
//   - eWallet — GoPay (GOPAY) and ShopeePay (SHOPEEPAY); the returned deeplink
//     and QR/redirect actions tell the payer how to complete the payment.
//   - Retail outlet — Alfamart and Indomaret (payment_type "cstore").
//   - Pay later — Akulaku (payment_type "akulaku").
//   - Card — credit_card via a token obtained on the client with Midtrans'
//     frontend SDK; the token is passed through metadata["card_token"].
//
// # Authentication
//
// Every request uses HTTP Basic Auth with the server key as the username and an
// empty password: Authorization: Basic base64("<server_key>:").
//
// # Callbacks
//
// Midtrans posts a single JSON notification shape for every product. The
// adapter authenticates it with the signature_key field, computed as
//
//	SHA512(order_id + status_code + gross_amount + server_key)
//
// and normalizes transaction_status (plus fraud_status for cards) onto a
// [provider.Status]. Midtrans retries until it receives any 2xx, which the
// webhook engine already replies with.
//
// # Configuration
//
// Keys are read from OPENPAY_PROVIDER_MIDTRANS_<KEY>:
//
//	server_key      Midtrans server key (required): "SB-Mid-server-..." on
//	                sandbox, "Mid-server-..." in production
//	base_url        https://api.sandbox.midtrans.com (default) or
//	                https://api.midtrans.com for production
//	callback_url    public URL of this platform's callback endpoint; appended
//	                to Midtrans' notification list per charge via the
//	                X-Append-Notification header (optional)
//	default_expiry  default payment lifetime as a Go duration (default "24h");
//	                applied to Virtual Accounts and retail outlets only, since
//	                Midtrans ignores custom_expiry for QRIS/e-wallet/card
//	timeout         per-request HTTP timeout (default "30s")
//
// # Idempotency
//
// Midtrans keys transactions by the merchant order_id, so the unified
// ReferenceID is passed straight through and a reused reference is rejected by
// the gateway as a duplicate (mapped to [provider.ErrDuplicateReference]). Both
// Inquiry and Cancel address the transaction by order_id, so no correlation
// cache is needed.
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
// URLs ("/webhooks/midtrans").
const Name = "midtrans"

// Endpoint paths, relative to the Core API base URL.
const (
	pathCharge = "/v2/charge"
)

// HTTP headers.
const (
	headerAuthorization      = "Authorization"
	headerAppendNotification = "X-Append-Notification"
)

// Defaults.
const (
	defaultBaseURL   = "https://api.sandbox.midtrans.com"
	defaultTimeout   = 30 * time.Second
	defaultExpiry    = 24 * time.Hour
	maxResponseBytes = 1 << 20 // 1 MiB
)

// Provider is the Midtrans adapter. It is safe for concurrent use.
type Provider struct {
	serverKey     string
	baseURL       string
	callbackURL   string
	defaultExpiry time.Duration
	client        *http.Client
}

// New builds a Midtrans adapter from configuration. See the package
// documentation for the recognized keys.
func New(cfg provider.Config) (*Provider, error) {
	serverKey, err := cfg.Required("server_key")
	if err != nil {
		return nil, err
	}

	p := &Provider{
		serverKey:     serverKey,
		baseURL:       strings.TrimRight(cfg.Get("base_url", defaultBaseURL), "/"),
		callbackURL:   strings.TrimSpace(cfg.Get("callback_url", "")),
		defaultExpiry: defaultExpiry,
		client:        &http.Client{Timeout: defaultTimeout},
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

// init registers the factory so the host application can build Midtrans from
// configuration alone.
func init() {
	provider.Register(Name, func(cfg provider.Config) (provider.Provider, error) {
		return New(cfg)
	})
}

// Name implements [provider.Provider].
func (p *Provider) Name() string { return Name }

// Capabilities implements [provider.Provider].
func (p *Provider) Capabilities() provider.Capabilities {
	channels := make([]string, 0, len(vaBanks)+len(ewalletTypes)+len(cstoreStores)+4)
	for code := range vaBanks {
		channels = append(channels, code)
	}
	channels = append(channels, channelMandiri, "QRIS")
	for code := range ewalletTypes {
		channels = append(channels, code)
	}
	for code := range cstoreStores {
		channels = append(channels, code)
	}
	channels = append(channels, channelAkulaku, channelCard)
	sort.Strings(channels)

	return provider.Capabilities{
		Methods: []provider.Method{
			provider.MethodVirtualAccount,
			provider.MethodQRIS,
			provider.MethodEWallet,
			provider.MethodRetailOutlet,
			provider.MethodPaylater,
			provider.MethodCard,
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
	if err := validateCreate(req); err != nil {
		return nil, err
	}
	payload, err := p.buildCharge(req)
	if err != nil {
		return nil, err
	}

	raw, err := p.call(ctx, http.MethodPost, pathCharge, payload, opCreate)
	if err != nil {
		return nil, err
	}
	var out transactionResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCreate, err)
	}

	return &provider.CreatePaymentResponse{
		ProviderReference: strings.TrimSpace(out.TransactionID),
		ReferenceID:       firstNonEmpty(out.OrderID, req.ReferenceID),
		Status:            mapTransactionStatus(out.TransactionStatus, out.FraudStatus),
		Amount:            amountOr(out.GrossAmount, req.Amount),
		PaymentCode:       paymentCode(&out),
		Actions:           createActions(&out),
		ExpiresAt:         parseTime(out.ExpiryTime),
		Raw:               raw,
	}, nil
}

func validateCreate(req *provider.CreatePaymentRequest) error {
	switch {
	case req == nil:
		return provider.NewError(Name, opCreate, "INVALID_REQUEST", "empty request", provider.ErrInvalidRequest)
	case strings.TrimSpace(req.ReferenceID) == "":
		return provider.NewError(Name, opCreate, "INVALID_REQUEST", "reference_id is required", provider.ErrInvalidRequest)
	case req.Amount <= 0:
		return provider.NewError(Name, opCreate, "INVALID_REQUEST", "amount must be positive", provider.ErrInvalidRequest)
	case !req.Method.IsValid():
		return provider.NewError(Name, opCreate, "INVALID_REQUEST", fmt.Sprintf("unknown method %q", req.Method), provider.ErrInvalidRequest)
	case req.Currency != "" && req.Currency != provider.IDR:
		return provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("midtrans only settles IDR, got %q", req.Currency), provider.ErrInvalidRequest)
	}
	return nil
}

// buildCharge assembles the Core API charge body for the requested method.
func (p *Provider) buildCharge(req *provider.CreatePaymentRequest) (*chargeRequest, error) {
	payload := &chargeRequest{
		TransactionDetails: transactionDetails{
			OrderID:     req.ReferenceID,
			GrossAmount: req.Amount,
		},
		CustomerDetails: customerDetailsFrom(req.Customer),
		ItemDetails:     itemDetailsFrom(req.Items),
		Metadata:        req.Metadata,
	}

	switch req.Method {
	case provider.MethodVirtualAccount:
		if err := p.buildVirtualAccount(payload, req); err != nil {
			return nil, err
		}
	case provider.MethodQRIS:
		payload.PaymentType = paymentTypeQRIS
		qris := &qrisDetail{Acquirer: strings.ToLower(strings.TrimSpace(req.Metadata["qris_acquirer"]))}
		payload.QRIS = qris
	case provider.MethodEWallet:
		if err := p.buildEWallet(payload, req); err != nil {
			return nil, err
		}
	case provider.MethodRetailOutlet:
		if err := p.buildRetailOutlet(payload, req); err != nil {
			return nil, err
		}
	case provider.MethodPaylater:
		channel := strings.ToUpper(strings.TrimSpace(req.Channel))
		if channel != "" && channel != channelAkulaku {
			return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
				fmt.Sprintf("unsupported paylater channel %q (only AKULAKU)", req.Channel), provider.ErrInvalidRequest)
		}
		payload.PaymentType = paymentTypeAkulaku
		payload.Akulaku = &akulakuDetail{}
	case provider.MethodCard:
		token := strings.TrimSpace(req.Metadata["card_token"])
		if token == "" {
			return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
				`card payments require metadata "card_token" from Midtrans' frontend SDK`, provider.ErrInvalidRequest)
		}
		payload.PaymentType = paymentTypeCreditCard
		payload.CreditCard = &creditCardDetail{TokenID: token, Authentication: true}
	default:
		return nil, provider.NewError(Name, opCreate, "UNSUPPORTED_METHOD",
			fmt.Sprintf("midtrans does not support method %q", req.Method), provider.ErrUnsupportedOperation)
	}
	return payload, nil
}

func (p *Provider) buildVirtualAccount(payload *chargeRequest, req *provider.CreatePaymentRequest) error {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	if channel == "" {
		return provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"virtual accounts require a channel code (e.g. BCA, BNI, BRI, PERMATA, CIMB, MANDIRI)", provider.ErrInvalidRequest)
	}
	if channel == channelMandiri {
		// Mandiri uses the echannel (Mandiri Bill Payment) product.
		payload.PaymentType = paymentTypeEChannel
		payload.EChannel = &echannelDetail{
			BillInfo1: "Payment",
			BillInfo2: descriptionOr(req),
		}
	} else {
		bank, ok := vaBanks[channel]
		if !ok {
			return provider.NewError(Name, opCreate, "INVALID_REQUEST",
				fmt.Sprintf("unsupported virtual account channel %q", req.Channel), provider.ErrInvalidRequest)
		}
		payload.PaymentType = paymentTypeBankTransfer
		payload.BankTransfer = &bankTransferDetail{Bank: bank}
	}
	payload.CustomExpiry = customExpiryFrom(p.expiry(req.Expiry))
	return nil
}

func (p *Provider) buildEWallet(payload *chargeRequest, req *provider.CreatePaymentRequest) error {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	paymentType, ok := ewalletTypes[channel]
	if !ok {
		return provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("unsupported e-wallet channel %q (GOPAY, SHOPEEPAY)", req.Channel), provider.ErrInvalidRequest)
	}
	payload.PaymentType = paymentType

	returnURL := firstNonEmpty(req.Metadata["return_url"], p.callbackURL)
	switch paymentType {
	case paymentTypeGoPay:
		gopay := &gopayDetail{}
		if returnURL != "" {
			gopay.EnableCallback = true
			gopay.CallbackURL = returnURL
		}
		payload.GoPay = gopay
	case paymentTypeShopeePay:
		payload.ShopeePay = &shopeepayDetail{CallbackURL: returnURL}
	}
	return nil
}

func (p *Provider) buildRetailOutlet(payload *chargeRequest, req *provider.CreatePaymentRequest) error {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	store, ok := cstoreStores[channel]
	if !ok {
		return provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("unsupported retail outlet channel %q (ALFAMART, INDOMARET)", req.Channel), provider.ErrInvalidRequest)
	}
	payload.PaymentType = paymentTypeCStore
	payload.CStore = &cstoreDetail{Store: store, Message: descriptionOr(req)}
	payload.CustomExpiry = customExpiryFrom(p.expiry(req.Expiry))
	return nil
}

// createActions augments Midtrans' actions with a display hint for the
// over-the-counter and e-channel flows (the biller/bill key pair).
func createActions(out *transactionResponse) []provider.PaymentAction {
	actions := mapActions(out.Actions)
	if strings.TrimSpace(out.BillKey) != "" {
		label := out.BillKey
		if strings.TrimSpace(out.BillerCode) != "" {
			label = "Biller code " + strings.TrimSpace(out.BillerCode) + ", bill key " + strings.TrimSpace(out.BillKey)
		}
		actions = append(actions, provider.PaymentAction{
			Type:        provider.ActionDisplay,
			Value:       label,
			Description: "Mandiri Bill Payment code",
		})
	}
	return actions
}

// --- inquiry ---------------------------------------------------------------

// Inquiry implements [provider.Provider]. It fetches the transaction status by
// the merchant reference (order_id) or the Midtrans transaction id.
func (p *Provider) Inquiry(ctx context.Context, req *provider.InquiryRequest) (*provider.InquiryResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if req == nil {
		return nil, provider.NewError(Name, opInquiry, "INVALID_REQUEST",
			"reference_id or provider_reference is required", provider.ErrInvalidRequest)
	}
	id := referenceID(req.ReferenceID, req.ProviderReference)
	if id == "" {
		return nil, provider.NewError(Name, opInquiry, "INVALID_REQUEST",
			"reference_id or provider_reference is required", provider.ErrInvalidRequest)
	}

	raw, err := p.call(ctx, http.MethodGet, "/v2/"+url.PathEscape(id)+"/status", nil, opInquiry)
	if err != nil {
		return nil, err
	}
	var out transactionResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if strings.EqualFold(strings.TrimSpace(out.TransactionStatus), "not_found") {
		return nil, provider.NewError(Name, opInquiry, "NOT_FOUND",
			"transaction not found", provider.ErrNotFound)
	}

	status := mapTransactionStatus(out.TransactionStatus, out.FraudStatus)
	return &provider.InquiryResponse{
		ReferenceID:       firstNonEmpty(out.OrderID, req.ReferenceID),
		ProviderReference: firstNonEmpty(out.TransactionID, req.ProviderReference),
		Status:            status,
		Amount:            amountOr(out.GrossAmount, 0),
		PaymentCode:       paymentCode(&out),
		PaidAt:            paidAtIfPaid(status, parseTime(firstNonEmpty(out.SettlementTime, out.TransactionTime))),
		ExpiresAt:         parseTime(out.ExpiryTime),
		Raw:               raw,
	}, nil
}

// --- cancel ----------------------------------------------------------------

// Cancel implements [provider.Provider]. It voids a pending transaction, or
// forces it to expire when the reason is "expire"; Midtrans offers a distinct
// endpoint for each.
func (p *Provider) Cancel(ctx context.Context, req *provider.CancelRequest) (*provider.CancelResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}
	if req == nil {
		return nil, provider.NewError(Name, opCancel, "INVALID_REQUEST",
			"reference_id or provider_reference is required", provider.ErrInvalidRequest)
	}
	id := referenceID(req.ReferenceID, req.ProviderReference)
	if id == "" {
		return nil, provider.NewError(Name, opCancel, "INVALID_REQUEST",
			"reference_id or provider_reference is required", provider.ErrInvalidRequest)
	}

	action := "cancel"
	fallback := provider.StatusCancelled
	if strings.EqualFold(strings.TrimSpace(req.Reason), "expire") {
		action = "expire"
		fallback = provider.StatusExpired
	}

	raw, err := p.call(ctx, http.MethodPost, "/v2/"+url.PathEscape(id)+"/"+action, nil, opCancel)
	if err != nil {
		return nil, err
	}
	var out transactionResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}

	status := mapTransactionStatus(out.TransactionStatus, out.FraudStatus)
	if status == provider.StatusUnknown {
		status = fallback
	}
	return &provider.CancelResponse{
		ReferenceID:       firstNonEmpty(out.OrderID, req.ReferenceID),
		ProviderReference: firstNonEmpty(out.TransactionID, req.ProviderReference),
		Status:            status,
		Raw:               raw,
	}, nil
}

// --- HTTP ------------------------------------------------------------------

// call signs nothing (Midtrans authenticates with Basic Auth) but sends one
// Core API request and returns the raw response body. Transport-level and
// HTTP-error responses are mapped onto contract sentinels; success responses
// are left to the caller to decode.
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
	if p.callbackURL != "" && path == pathCharge {
		// Append our callback to the merchant's configured notification URLs.
		req.Header.Set(headerAppendNotification, p.callbackURL)
	}

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
	// Midtrans normally mirrors status_code in the HTTP status, but guard
	// against a 2xx envelope that still carries a failure code.
	if err := envelopeError(op, resp.StatusCode, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// envelopeError inspects the status_code of an otherwise-successful response
// and converts a non-2xx code into a [*provider.Error]. It is a no-op when the
// body is not JSON or carries no status code.
func envelopeError(op string, httpStatus int, raw []byte) error {
	var envelope transactionResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	code := strings.TrimSpace(envelope.StatusCode)
	if code == "" || strings.HasPrefix(code, "2") {
		return nil
	}
	status := httpStatus
	if n, err := strconv.Atoi(code); err == nil {
		status = n
	}
	return apiError(op, status, raw)
}

// apiError turns an unsuccessful Midtrans response into a [*provider.Error],
// keeping the gateway's status_code and message.
func apiError(op string, httpStatus int, raw []byte) error {
	var envelope transactionResponse
	_ = json.Unmarshal(raw, &envelope)

	code := firstNonEmpty(envelope.StatusCode, strconv.Itoa(httpStatus))
	message := firstNonEmpty(envelope.StatusMessage, truncate(string(raw)))
	return &provider.Error{
		Provider:   Name,
		Op:         op,
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
		Err:        classifyStatus(httpStatus, code),
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

// --- helpers ---------------------------------------------------------------

// expiry returns the requested lifetime, or the configured default.
func (p *Provider) expiry(requested time.Duration) time.Duration {
	if requested > 0 {
		return requested
	}
	return p.defaultExpiry
}

// referenceID picks the identifier Midtrans should address the transaction by,
// preferring the merchant reference (order_id).
func referenceID(reference, providerReference string) string {
	return firstNonEmpty(reference, providerReference)
}

// descriptionOr returns the payment description, falling back to the merchant
// reference so gateway fields that require text are never empty.
func descriptionOr(req *provider.CreatePaymentRequest) string {
	if d := strings.TrimSpace(req.Description); d != "" {
		return d
	}
	return req.ReferenceID
}
