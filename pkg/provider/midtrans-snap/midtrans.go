// Package midtranssnap implements the [provider.Provider] contract for
// Midtrans (midtrans.com), one of Indonesia's largest payment gateways. Its
// provider name is "midtrans-snap"; the legacy, non-SNAP Core API
// (/v2/charge) is implemented by the sibling package
// pkg/provider/midtrans.
//
// It targets Midtrans' BI-SNAP Core API — the Bank Indonesia "Standar Nasional
// Open API Pembayaran" interface documented at
// https://docs.midtrans.com/reference/core-api-snap-open-api-overview — and not
// the legacy Core API (/v2/charge) nor the hosted Snap checkout product. It
// covers:
//
//   - Virtual Account — bank transfer for Permata, BCA, Mandiri, CIMB, BNI, BRI,
//     Danamon, BSI, SeaBank and Saqu.
//   - QRIS — the merchant-presented dynamic QR.
//   - eWallet — GoPay, ShopeePay and DANA via the direct-debit host-to-host
//     flow.
//
// # Authentication
//
// Midtrans' BI-SNAP flow uses two signature schemes. First a B2B access token is
// obtained from /v1.0/access-token/b2b, signing with RSA-SHA256 (PKCS#1 v1.5)
// over "ClientID|Timestamp" using the merchant's private key. Every
// transactional request then carries an HMAC-SHA512 signature in the
// X-SIGNATURE header, computed with the merchant's client_secret over
//
//	HTTPMethod:EndpointUrl:AccessToken:Lowercase(HexEncode(SHA-256(minify(body)))):X-TIMESTAMP
//
// and an Authorization: Bearer <token> header. The access token is cached and
// refreshed shortly before its 900s expiry, and once more on a 401.
//
// Inbound callbacks are signed by Midtrans with RSA-SHA256 over the same shape
// (without the access token) and verified with Midtrans' public key; see
// [Provider.VerifyWebhookSignature].
//
// # Configuration
//
// Keys are read from OPENPAY_PROVIDER_MIDTRANS_SNAP_<KEY>:
//
//	client_id        Midtrans client ID (required) — X-CLIENT-KEY
//	client_secret    Midtrans client secret, the HMAC key (required)
//	private_key      our RSA private key for signing the get-token request
//	                 (required): a PEM block or single-line base64 DER
//	public_key       Midtrans' RSA public key, for verifying callbacks; when
//	                 empty, callbacks are rejected
//	base_url         https://merchants.sbx.midtrans.com (default) or
//	                 https://merchants.midtrans.com for production
//	partner_id       X-PARTNER-ID value (default: client_id)
//	merchant_id      Midtrans merchant ID, required for VA; the default QRIS
//	                 merchant id when a payment does not override it
//	partner_service_id 8-char left-space-padded BIN / company code, required
//	                 for Virtual Account
//	channel_id       CHANNEL-ID header value, 5 numeric digits (default "12345")
//	qris_acquirer    QRIS acquirer: GOPAY (default), AIRPAY or SHOPEE
//	device_id        X-DEVICE-ID header value (optional)
//	default_expiry   payment lifetime as a Go duration (default "24h")
//	timeout          per-request HTTP timeout (default "30s")
//
// # Per-payment overrides
//
// Two gateway values are supplied per payment through the unified
// [provider.CreatePaymentRequest].Metadata map, because they are not part of
// the gateway-agnostic contract:
//
//	return_url       e-wallet only: the browser URL the payer returns to after
//	                 completing the payment (mapped to urlParams[].url with type
//	                 PAY_RETURN). Without it, Midtrans uses the PAY_RETURN url
//	                 configured in the merchant dashboard.
//	sub_merchant_id  QRIS only: the merchant id that is unique per (sub)merchant
//	                 and addresses the transaction. Falls back to the configured
//	                 merchant_id when omitted.
//
// # Correlation cache
//
// Midtrans' status and cancel endpoints are addressed by identifiers (the VA
// number, the original partner/external reference numbers) that the unified
// contract does not carry between calls. X-EXTERNAL-ID is Midtrans' per-request
// idempotency key and is later replayed as originalExternalId, so — like the
// DOKU and Winpay adapters — this adapter keeps a short-lived in-process cache
// filled by CreatePayment. A restart loses it; a production deployment would
// persist these identifiers alongside the transaction.
//
// # Sandbox status
//
// Only the sandbox base URL is the default. Channel availability is
// account-specific and has NOT been verified against the live Midtrans sandbox.
package midtranssnap

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// Name is the provider identifier used in configuration, routing and callback
// URLs ("/webhooks/midtrans-snap").
const Name = "midtrans-snap"

// Operation names used in [provider.Error].
const (
	opCreate  = "CreatePayment"
	opInquiry = "Inquiry"
	opCancel  = "Cancel"
	opWebhook = "ParseWebhook"
	opVerify  = "VerifyWebhookSignature"
	opToken   = "GetToken"
)

// SNAP header names.
const (
	headerTimestamp  = "X-TIMESTAMP"
	headerSignature  = "X-SIGNATURE"
	headerClientID   = "X-CLIENT-KEY"
	headerPartnerID  = "X-PARTNER-ID"
	headerExternalID = "X-EXTERNAL-ID"
	headerChannelID  = "CHANNEL-ID"
	headerDeviceID   = "X-DEVICE-ID"
)

// Callback event types produced by [Provider.ParseWebhook]. They also select
// the acknowledgement body in [Provider.WebhookResponse].
const (
	EventVAPayment      = "transfer-va.payment"
	EventEWalletPayment = "ewallet.payment"
	EventQRISPayment    = "qris.payment"
)

// SNAP acknowledgement codes Midtrans expects from the merchant callback URL.
const (
	responseVAPayment      = "2002500"
	responseEWalletPayment = "2005600"
	responseQRISPayment    = "2005200"
)

// Midtrans BI-SNAP endpoint paths, relative to the base URL.
const (
	pathAccessToken = "/v1.0/access-token/b2b"

	pathVACreate = "/v1.0/transfer-va/create-va"
	pathVAStatus = "/v1.0/transfer-va/status"
	pathVADelete = "/v1.0/transfer-va/delete-va"

	pathQRISGenerate = "/v1.0/qr/qr-mpm-generate"
	pathQRISQuery    = "/v1.0/qr/qr-mpm-query"
	pathQRISCancel   = "/v1.0/qr/qr-mpm-cancel"

	pathDebitCreate = "/v1.0/debit/payment-host-to-host"
	pathDebitStatus = "/v1.0/debit/status"
	pathDebitCancel = "/v1.0/debit/cancel"

	// Notification paths (Midtrans -> merchant). Used only as a fallback when
	// the webhook engine does not supply the request path.
	pathVANotify   = "/v1.0/transfer-va/payment"
	pathQRISNotify = "/v1.0/qr/qr-mpm-notify"
	pathDBNotify   = "/v1.0/debit/notify"
)

// Defaults.
const (
	defaultBaseURL   = "https://merchants.sbx.midtrans.com"
	defaultChannelID = "12345"
	defaultQRISAcq   = "gopay"
	defaultTimeout   = 30 * time.Second
	defaultExpiry    = 24 * time.Hour
	maxResponseBytes = 1 << 20 // 1 MiB
)

// tokenSkew is how long before the reported expiry the cached token is
// considered stale.
const tokenSkew = 60 * time.Second

// Record cache tuning.
const (
	recordTTL  = 24 * time.Hour
	pruneEvery = time.Minute
)

// Provider implements [provider.Provider] for Midtrans' BI-SNAP Core API.
type Provider struct {
	clientID     string
	clientSecret string
	privateKey   *rsa.PrivateKey
	publicKey    *rsa.PublicKey

	baseURL       string
	partnerID     string
	merchantID    string
	partnerSvcID  string
	channelID     string
	deviceID      string
	qrAcquirer    string
	defaultExpiry time.Duration
	client        *http.Client

	mu        sync.RWMutex
	records   map[string]*record
	lastPrune time.Time

	tokenMu     sync.Mutex
	token       string
	tokenExpiry time.Time
}

// record caches the gateway identifiers that the unified contract does not
// carry between calls.
type record struct {
	providerRef string
	paymentCode string
	channel     string
	method      provider.Method
	trxID       string
	customerNo  string
	merchantID  string
	externalID  string
	amount      provider.Amount
	createdAt   time.Time
}

// New builds a Midtrans adapter from configuration. See the package
// documentation for the recognized keys.
func New(cfg provider.Config) (*Provider, error) {
	clientID, err := cfg.Required("client_id")
	if err != nil {
		return nil, err
	}
	clientSecret, err := cfg.Required("client_secret")
	if err != nil {
		return nil, err
	}
	privateKeyRaw, err := cfg.Required("private_key")
	if err != nil {
		return nil, err
	}
	privateKey, err := parsePrivateKey(privateKeyRaw)
	if err != nil {
		return nil, err
	}

	p := &Provider{
		clientID:      clientID,
		clientSecret:  clientSecret,
		privateKey:    privateKey,
		baseURL:       strings.TrimRight(cfg.Get("base_url", defaultBaseURL), "/"),
		partnerID:     strings.TrimSpace(cfg.Get("partner_id", "")),
		merchantID:    strings.TrimSpace(cfg.Get("merchant_id", "")),
		partnerSvcID:  strings.TrimRight(cfg.Get("partner_service_id", ""), " \t\r\n"),
		channelID:     strings.TrimSpace(cfg.Get("channel_id", defaultChannelID)),
		deviceID:      strings.TrimSpace(cfg.Get("device_id", "")),
		qrAcquirer:    strings.ToUpper(strings.TrimSpace(cfg.Get("qris_acquirer", defaultQRISAcq))),
		defaultExpiry: defaultExpiry,
		client:        provider.NewHTTPClient(Name, defaultTimeout),
		records:       make(map[string]*record),
	}
	if p.partnerID == "" {
		p.partnerID = p.clientID
	}
	if p.channelID == "" {
		p.channelID = defaultChannelID
	}

	if raw := strings.TrimSpace(cfg.Get("public_key", "")); raw != "" {
		pub, err := parsePublicKey(raw)
		if err != nil {
			return nil, err
		}
		p.publicKey = pub
	}
	if raw := cfg.Get("timeout", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("midtrans-snap: invalid timeout %q: %w", raw, err)
		}
		p.client.Timeout = d
	}
	if raw := cfg.Get("default_expiry", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("midtrans-snap: invalid default_expiry %q: %w", raw, err)
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
	channels := make([]string, 0, len(vaBanks)+len(ewalletChannels)+1)
	for code := range vaBanks {
		channels = append(channels, code)
	}
	channels = append(channels, "QRIS")
	for code := range ewalletChannels {
		channels = append(channels, code)
	}
	sort.Strings(channels)

	return provider.Capabilities{
		Methods: []provider.Method{
			provider.MethodVirtualAccount,
			provider.MethodQRIS,
			provider.MethodEWallet,
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
	default:
		return nil, provider.NewError(Name, opCreate, "UNSUPPORTED_METHOD",
			fmt.Sprintf("midtrans BI-SNAP supports virtual account, QRIS and e-wallet, not %q", req.Method),
			provider.ErrUnsupportedOperation)
	}
}

func (p *Provider) createVA(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	if channel == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"virtual accounts require a channel code (e.g. BCA, BNI, MANDIRI)", provider.ErrInvalidRequest)
	}
	bank, ok := vaBanks[channel]
	if !ok {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("unsupported virtual account channel %q", channel), provider.ErrInvalidRequest)
	}
	if p.partnerSvcID == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"virtual accounts require the partner_service_id config", provider.ErrInvalidRequest)
	}
	if p.merchantID == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"virtual accounts require the merchant_id config", provider.ErrInvalidRequest)
	}

	custNo := strings.TrimSpace(req.Metadata["customer_no"])
	if custNo == "" {
		custNo = customerNo(referenceID(req))
	}

	payload := createVARequest{
		PartnerServiceID:   p.partnerSvcID,
		CustomerNo:         custNo,
		VirtualAccountNo:   p.partnerSvcID + custNo,
		VirtualAccountName: accountName(req.Customer.Name, req.ReferenceID),
		TrxID:              req.ReferenceID,
		TotalAmount:        amountPayloadOf(req.Amount),
		AdditionalInfo: vaAdditional{
			MerchantID: p.merchantID,
			Bank:       bank,
		},
	}
	if email := strings.TrimSpace(req.Customer.Email); email != "" {
		payload.VirtualAccountEmail = email
	}
	if phone := strings.TrimSpace(req.Customer.Phone); phone != "" {
		payload.VirtualAccountPhone = phone
	}
	if expiry := p.expiry(req.Expiry); !expiry.IsZero() {
		payload.ExpiredDate = expiry.In(jakarta).Format(iso8601Layout)
	}
	if channel == "MANDIRI" {
		payload.AdditionalInfo.Mandiri = &mandiriDetail{
			BillInfo1: "Payment",
			BillInfo2: descriptionOr(req),
		}
	}

	raw, err := p.call(ctx, http.MethodPost, pathVACreate, req.ReferenceID, payload, opCreate)
	if err != nil {
		return nil, err
	}
	var out createVAResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCreate, err)
	}
	if err := checkCode(opCreate, out.apiResponse); err != nil {
		return nil, err
	}

	data := out.VirtualAccountData
	vaNo := strings.TrimSpace(data.VirtualAccountNo)
	if vaNo == "" {
		vaNo = payload.VirtualAccountNo
	}
	amount := amountOr(data.TotalAmount, req.Amount)
	reference := firstNonEmpty(data.TrxID, req.ReferenceID)

	p.remember(reference, &record{
		providerRef: vaNo,
		paymentCode: vaNo,
		channel:     channel,
		method:      provider.MethodVirtualAccount,
		trxID:       reference,
		customerNo:  firstNonEmpty(data.CustomerNo, custNo),
		externalID:  req.ReferenceID,
		amount:      amount,
	})

	var actions []provider.PaymentAction
	if vaNo != "" {
		actions = append(actions, provider.PaymentAction{
			Type:        provider.ActionDisplay,
			Value:       vaNo,
			Description: "Virtual account number",
		})
	}
	return &provider.CreatePaymentResponse{
		ProviderReference: vaNo,
		ReferenceID:       reference,
		Status:            provider.StatusPending,
		Amount:            amount,
		PaymentCode:       vaNo,
		Actions:           actions,
		ExpiresAt:         parseTime(data.ExpiredDate),
		Raw:               raw,
	}, nil
}

func (p *Provider) createQRIS(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	if channel := strings.ToUpper(strings.TrimSpace(req.Channel)); channel != "" && channel != "QRIS" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("qris channel must be \"QRIS\", got %q", req.Channel), provider.ErrInvalidRequest)
	}
	// QRIS is addressed by a merchant id that is unique per merchant, which the
	// caller supplies per payment via metadata["sub_merchant_id"]; it is not the
	// same value the account uses for virtual accounts. The configured
	// merchant_id is only a fallback for callers that do not vary it.
	merchantID := p.qrisMerchantID(req)
	if merchantID == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"qris requires the merchant_id config or a metadata sub_merchant_id", provider.ErrInvalidRequest)
	}

	payload := createQRISRequest{
		PartnerReferenceNo: req.ReferenceID,
		Amount:             amountPayloadOf(req.Amount),
		MerchantID:         merchantID,
		AdditionalInfo:     &qrisAdditional{Acquirer: strings.ToLower(p.qrAcquirer)},
	}
	if expiry := p.expiry(req.Expiry); !expiry.IsZero() {
		payload.ValidityPeriod = expiry.In(jakarta).Format(iso8601Layout)
	}

	raw, err := p.call(ctx, http.MethodPost, pathQRISGenerate, req.ReferenceID, payload, opCreate)
	if err != nil {
		return nil, err
	}
	var out createQRISResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCreate, err)
	}
	if err := checkCode(opCreate, out.apiResponse); err != nil {
		return nil, err
	}

	reference := firstNonEmpty(out.PartnerReferenceNo, req.ReferenceID)
	providerRef := firstNonEmpty(out.ReferenceNo, reference)

	p.remember(reference, &record{
		providerRef: providerRef,
		channel:     "QRIS",
		method:      provider.MethodQRIS,
		trxID:       reference,
		merchantID:  merchantID,
		externalID:  req.ReferenceID,
		amount:      req.Amount,
	})

	var actions []provider.PaymentAction
	if content := strings.TrimSpace(out.QRContent); content != "" {
		actions = append(actions, provider.PaymentAction{
			Type:        provider.ActionQRCode,
			Value:       content,
			Description: "QRIS payload to render",
		})
	}
	if url := strings.TrimSpace(out.QRURL); url != "" {
		actions = append(actions, provider.PaymentAction{
			Type:        provider.ActionURL,
			Value:       url,
			Description: "QRIS image",
		})
	}
	return &provider.CreatePaymentResponse{
		ProviderReference: providerRef,
		ReferenceID:       reference,
		Status:            provider.StatusPending,
		Amount:            req.Amount,
		PaymentCode:       strings.TrimSpace(out.QRContent),
		Actions:           actions,
		Raw:               raw,
	}, nil
}

// qrisMerchantID resolves the merchant id Midtrans addresses a QRIS
// transaction by. Unlike virtual accounts, QRIS is routed with an id that is
// unique per (sub)merchant, which the caller supplies per payment via
// metadata["sub_merchant_id"]. The configured merchant_id is only a fallback
// for accounts that do not vary it.
func (p *Provider) qrisMerchantID(req *provider.CreatePaymentRequest) string {
	if req != nil {
		if id := strings.TrimSpace(req.Metadata["sub_merchant_id"]); id != "" {
			return id
		}
	}
	return p.merchantID
}

func (p *Provider) createEWallet(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	if channel == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"e-wallets require a channel code (GOPAY, SHOPEEPAY, DANA)", provider.ErrInvalidRequest)
	}
	payOption, ok := ewalletChannels[channel]
	if !ok {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("unsupported e-wallet channel %q (GOPAY, SHOPEEPAY, DANA)", req.Channel), provider.ErrInvalidRequest)
	}

	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	payload := createEWalletRequest{
		PartnerReferenceNo: req.ReferenceID,
		ChargeToken:        token,
		MerchantID:         p.merchantID,
		PayOptionDetails: []payOptionDetail{{
			PayMethod:   payOption,
			PayOption:   payOption,
			TransAmount: amountPayloadOf(req.Amount),
		}},
	}
	if expiry := p.expiry(req.Expiry); !expiry.IsZero() {
		payload.ValidUpTo = expiry.In(jakarta).Format(iso8601Layout)
	}
	// The e-wallet PAY_RETURN url is where the payer is redirected after
	// completing payment, so it must come from the payment request, not from the
	// gateway callback/webhook configuration.
	if returnURL := strings.TrimSpace(req.Metadata["return_url"]); returnURL != "" {
		payload.URLParams = []ewalletURLParam{{URL: returnURL, Type: "PAY_RETURN", IsDeeplink: "N"}}
	}
	if details := customerDetailsFrom(req.Customer); details != nil {
		payload.AdditionalInfo = &ewalletAdditional{CustomerDetails: details}
	}

	extID := externalID()
	raw, err := p.call(ctx, http.MethodPost, pathDebitCreate, extID, payload, opCreate)
	if err != nil {
		return nil, err
	}
	var out createEWalletResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCreate, err)
	}
	if err := checkCode(opCreate, out.apiResponse); err != nil {
		return nil, err
	}

	reference := firstNonEmpty(out.PartnerReferenceNo, req.ReferenceID)
	providerRef := firstNonEmpty(out.ReferenceNo, reference)

	p.remember(reference, &record{
		providerRef: providerRef,
		channel:     channel,
		method:      provider.MethodEWallet,
		trxID:       reference,
		externalID:  extID,
		amount:      req.Amount,
	})

	var actions []provider.PaymentAction
	if url := strings.TrimSpace(out.AppRedirectURL); url != "" {
		actions = append(actions, provider.PaymentAction{
			Type:        provider.ActionDeeplink,
			Value:       url,
			Description: "Open the e-wallet app",
		})
	}
	if url := strings.TrimSpace(out.WebRedirectURL); url != "" {
		actions = append(actions, provider.PaymentAction{
			Type:        provider.ActionURL,
			Value:       url,
			Description: "Complete the payment in the e-wallet",
		})
	}
	return &provider.CreatePaymentResponse{
		ProviderReference: providerRef,
		ReferenceID:       reference,
		Status:            provider.StatusPending,
		Amount:            req.Amount,
		Actions:           actions,
		Raw:               raw,
	}, nil
}

// --- inquiry ---------------------------------------------------------------

// Inquiry implements [provider.Provider].
func (p *Provider) Inquiry(ctx context.Context, req *provider.InquiryRequest) (*provider.InquiryResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if req == nil || (strings.TrimSpace(req.ReferenceID) == "" && strings.TrimSpace(req.ProviderReference) == "") {
		return nil, provider.NewError(Name, opInquiry, "INVALID_REQUEST",
			"reference_id or provider_reference is required", provider.ErrInvalidRequest)
	}
	rec := p.lookup(req.ReferenceID, req.ProviderReference)
	if rec == nil {
		return nil, provider.NewError(Name, opInquiry, "UNKNOWN_REFERENCE",
			"transaction is not known to this instance: midtrans status needs the identifiers captured at creation",
			provider.ErrNotFound)
	}

	switch rec.method {
	case provider.MethodVirtualAccount:
		return p.inquireVA(ctx, req, rec)
	case provider.MethodQRIS:
		return p.inquireQRIS(ctx, req, rec)
	case provider.MethodEWallet:
		return p.inquireEWallet(ctx, req, rec)
	default:
		return nil, provider.NewError(Name, opInquiry, "UNSUPPORTED_METHOD",
			fmt.Sprintf("cannot inquire method %q", rec.method), provider.ErrUnsupportedOperation)
	}
}

func (p *Provider) inquireVA(ctx context.Context, req *provider.InquiryRequest, rec *record) (*provider.InquiryResponse, error) {
	trxID := firstNonEmpty(rec.trxID, req.ReferenceID)
	vaNo := strings.TrimSpace(rec.paymentCode)

	payload := vaStatusRequest{
		PartnerServiceID: p.partnerSvcID,
		CustomerNo:       firstNonEmpty(rec.customerNo, strings.TrimPrefix(vaNo, p.partnerSvcID)),
		VirtualAccountNo: vaNo,
		InquiryRequestID: trxID,
	}
	payload.AdditionalInfo.MerchantID = p.merchantID

	raw, err := p.call(ctx, http.MethodPost, pathVAStatus, externalID(), payload, opInquiry)
	if err != nil {
		return nil, err
	}
	var out vaStatusResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if err := checkCode(opInquiry, out.apiResponse); err != nil {
		return nil, err
	}

	status := mapVAStatus(out.VirtualAccountData)
	return &provider.InquiryResponse{
		ReferenceID:       firstNonEmpty(out.VirtualAccountData.InquiryRequestID, trxID),
		ProviderReference: vaNo,
		Status:            status,
		Amount:            amountOr(out.VirtualAccountData.PaidAmount, amountOr(out.VirtualAccountData.TotalAmount, rec.amount)),
		PaymentCode:       firstNonEmpty(strings.TrimSpace(out.VirtualAccountData.VirtualAccountNo), vaNo),
		PaidAt:            paidAtIfPaid(status, parseTime(firstNonEmpty(out.VirtualAccountData.TrxDateTime, out.VirtualAccountData.TransactionDate))),
		Raw:               raw,
	}, nil
}

func (p *Provider) inquireQRIS(ctx context.Context, req *provider.InquiryRequest, rec *record) (*provider.InquiryResponse, error) {
	trxID := firstNonEmpty(rec.trxID, req.ReferenceID)
	merchantID := firstNonEmpty(rec.merchantID, p.merchantID)
	if merchantID == "" {
		return nil, provider.NewError(Name, opInquiry, "INVALID_REQUEST",
			"qris requires the merchant id captured at creation", provider.ErrInvalidRequest)
	}
	payload := queryQRISRequest{
		OriginalPartnerReferenceNo: trxID,
		OriginalReferenceNo:        rec.providerRef,
		OriginalExternalID:         rec.externalID,
		MerchantID:                 merchantID,
		ServiceCode:                qrisServiceCode,
	}
	raw, err := p.call(ctx, http.MethodPost, pathQRISQuery, externalID(), payload, opInquiry)
	if err != nil {
		return nil, err
	}
	var out queryQRISResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if err := checkCode(opInquiry, out.apiResponse); err != nil {
		return nil, err
	}
	status := mapTransactionStatus(out.LatestTransactionStatus)
	return &provider.InquiryResponse{
		ReferenceID:       firstNonEmpty(out.OriginalPartnerReferenceNo, trxID),
		ProviderReference: firstNonEmpty(out.OriginalReferenceNo, rec.providerRef),
		Status:            status,
		Amount:            amountOr(out.Amount, rec.amount),
		PaidAt:            paidAtIfPaid(status, parseTime(out.PaidTime)),
		Raw:               raw,
	}, nil
}

func (p *Provider) inquireEWallet(ctx context.Context, req *provider.InquiryRequest, rec *record) (*provider.InquiryResponse, error) {
	trxID := firstNonEmpty(rec.trxID, req.ReferenceID)
	payload := ewalletStatusRequest{
		OriginalPartnerReferenceNo: trxID,
		OriginalExternalID:         firstNonEmpty(rec.externalID, trxID),
		ServiceCode:                debitServiceCode,
	}
	if rec.providerRef != "" {
		payload.OriginalReferenceNo = rec.providerRef
	}
	raw, err := p.call(ctx, http.MethodPost, pathDebitStatus, externalID(), payload, opInquiry)
	if err != nil {
		return nil, err
	}
	var out ewalletStatusResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if err := checkCode(opInquiry, out.apiResponse); err != nil {
		return nil, err
	}
	status := mapTransactionStatus(out.LatestTransactionStatus)
	return &provider.InquiryResponse{
		ReferenceID:       firstNonEmpty(out.OriginalPartnerReferenceNo, trxID),
		ProviderReference: firstNonEmpty(out.OriginalReferenceNo, rec.providerRef),
		Status:            status,
		Amount:            amountOr(out.TransAmount, rec.amount),
		PaidAt:            paidAtIfPaid(status, parseTime(out.PaidTime)),
		Raw:               raw,
	}, nil
}

// --- cancel ----------------------------------------------------------------

// Cancel implements [provider.Provider]. Midtrans' BI-SNAP exposes a single
// cancel operation per product: it voids a pending transaction. The "expire"
// convention is not a distinct gateway action, so a successful cancel is always
// reported as cancelled.
func (p *Provider) Cancel(ctx context.Context, req *provider.CancelRequest) (*provider.CancelResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}
	if req == nil || (strings.TrimSpace(req.ReferenceID) == "" && strings.TrimSpace(req.ProviderReference) == "") {
		return nil, provider.NewError(Name, opCancel, "INVALID_REQUEST",
			"reference_id or provider_reference is required", provider.ErrInvalidRequest)
	}
	rec := p.lookup(req.ReferenceID, req.ProviderReference)
	if rec == nil {
		return nil, provider.NewError(Name, opCancel, "UNKNOWN_REFERENCE",
			"transaction is not known to this instance: midtrans cancel needs the identifiers captured at creation",
			provider.ErrNotFound)
	}

	switch rec.method {
	case provider.MethodVirtualAccount:
		return p.cancelVA(ctx, rec)
	case provider.MethodQRIS:
		return p.cancelQRIS(ctx, rec, req.Reason)
	case provider.MethodEWallet:
		return p.cancelEWallet(ctx, rec)
	default:
		return nil, provider.NewError(Name, opCancel, "UNSUPPORTED_METHOD",
			fmt.Sprintf("cannot cancel method %q", rec.method), provider.ErrUnsupportedOperation)
	}
}

func (p *Provider) cancelVA(ctx context.Context, rec *record) (*provider.CancelResponse, error) {
	trxID := firstNonEmpty(rec.trxID, "")
	vaNo := strings.TrimSpace(rec.paymentCode)
	if vaNo == "" {
		return nil, provider.NewError(Name, opCancel, "NOT_FOUND",
			"cannot resolve the virtual account number: no cached record", provider.ErrNotFound)
	}

	payload := deleteVARequest{
		PartnerServiceID: p.partnerSvcID,
		CustomerNo:       firstNonEmpty(rec.customerNo, strings.TrimPrefix(vaNo, p.partnerSvcID)),
		VirtualAccountNo: vaNo,
		TrxID:            trxID,
	}
	payload.AdditionalInfo.MerchantID = p.merchantID

	raw, err := p.call(ctx, http.MethodPost, pathVADelete, externalID(), payload, opCancel)
	if err != nil {
		return nil, err
	}
	var out deleteVAResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}
	if err := checkCode(opCancel, out.apiResponse); err != nil {
		return nil, err
	}
	return &provider.CancelResponse{
		ReferenceID:       trxID,
		ProviderReference: vaNo,
		Status:            provider.StatusCancelled,
		Raw:               raw,
	}, nil
}

func (p *Provider) cancelQRIS(ctx context.Context, rec *record, reason string) (*provider.CancelResponse, error) {
	trxID := firstNonEmpty(rec.trxID, "")
	merchantID := firstNonEmpty(rec.merchantID, p.merchantID)
	if merchantID == "" {
		return nil, provider.NewError(Name, opCancel, "INVALID_REQUEST",
			"qris requires the merchant id captured at creation", provider.ErrInvalidRequest)
	}
	payload := cancelQRISRequest{
		OriginalPartnerReferenceNo: trxID,
		OriginalReferenceNo:        rec.providerRef,
		OriginalExternalID:         rec.externalID,
		MerchantID:                 merchantID,
		Reason:                     firstNonEmpty(reason, "cancel"),
		Amount:                     amountPayloadOf(rec.amount),
	}
	raw, err := p.call(ctx, http.MethodPost, pathQRISCancel, externalID(), payload, opCancel)
	if err != nil {
		return nil, err
	}
	var out cancelQRISResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}
	if err := checkCode(opCancel, out.apiResponse); err != nil {
		return nil, err
	}
	return &provider.CancelResponse{
		ReferenceID:       trxID,
		ProviderReference: firstNonEmpty(out.OriginalReferenceNo, rec.providerRef),
		Status:            provider.StatusCancelled,
		Raw:               raw,
	}, nil
}

func (p *Provider) cancelEWallet(ctx context.Context, rec *record) (*provider.CancelResponse, error) {
	trxID := firstNonEmpty(rec.trxID, "")
	payload := cancelEWalletRequest{
		OriginalReferenceNo:        rec.providerRef,
		OriginalExternalID:         firstNonEmpty(rec.externalID, trxID),
		OriginalPartnerReferenceNo: trxID,
	}
	raw, err := p.call(ctx, http.MethodPost, pathDebitCancel, externalID(), payload, opCancel)
	if err != nil {
		return nil, err
	}
	var out cancelEWalletResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}
	if err := checkCode(opCancel, out.apiResponse); err != nil {
		return nil, err
	}
	return &provider.CancelResponse{
		ReferenceID:       trxID,
		ProviderReference: firstNonEmpty(out.OriginalReferenceNo, rec.providerRef),
		Status:            provider.StatusCancelled,
		Raw:               raw,
	}, nil
}

// --- access token ----------------------------------------------------------

// accessToken returns a valid B2B access token, fetching a new one when the
// cached token is missing or within tokenSkew of its expiry. It is safe for
// concurrent use.
func (p *Provider) accessToken(ctx context.Context) (string, error) {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()
	if p.token != "" && time.Now().Before(p.tokenExpiry.Add(-tokenSkew)) {
		return p.token, nil
	}
	token, expiresIn, err := p.fetchToken(ctx)
	if err != nil {
		return "", err
	}
	p.token = token
	p.tokenExpiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
	return token, nil
}

// fetchToken calls the get-token endpoint with an RSA-signed request.
func (p *Provider) fetchToken(ctx context.Context) (string, int, error) {
	timestamp := time.Now().In(jakarta).Format(iso8601Layout)
	signature, err := signToken(p.privateKey, p.clientID, timestamp)
	if err != nil {
		return "", 0, provider.NewError(Name, opToken, "SIGN", err.Error(), provider.ErrGateway)
	}
	body, err := marshal(tokenRequest{GrantType: "client_credentials"})
	if err != nil {
		return "", 0, provider.NewError(Name, opToken, "ENCODE", err.Error(), provider.ErrInvalidRequest)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+pathAccessToken, bytes.NewReader(body))
	if err != nil {
		return "", 0, provider.NewError(Name, opToken, "REQUEST", err.Error(), provider.ErrGateway)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(headerClientID, p.clientID)
	req.Header.Set(headerTimestamp, timestamp)
	req.Header.Set(headerSignature, signature)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", 0, provider.NewError(Name, opToken, "NETWORK", err.Error(), provider.ErrGateway)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", 0, provider.NewError(Name, opToken, "READ", err.Error(), provider.ErrGateway)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", 0, provider.NewError(Name, opToken, strconv.Itoa(resp.StatusCode),
			"midtrans rejected our credentials", provider.ErrUnauthorized)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return "", 0, provider.NewError(Name, opToken, strconv.Itoa(resp.StatusCode), truncate(string(raw)), provider.ErrGateway)
	}

	var out tokenResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, provider.NewError(Name, opToken, "BAD_JSON", "cannot decode access-token response: "+err.Error(), provider.ErrInvalidRequest)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.ResponseCode), "200") {
		return "", 0, provider.NewError(Name, opToken, out.ResponseCode, firstNonEmpty(out.ResponseMessage, "access-token request failed"), classifyCode(out.ResponseCode))
	}
	if token := strings.TrimSpace(out.AccessToken); token != "" {
		expiresIn := int(out.ExpiresIn)
		if expiresIn <= 0 {
			expiresIn = 900
		}
		return token, expiresIn, nil
	}
	return "", 0, provider.NewError(Name, opToken, "NO_TOKEN", "access-token response carried no access token", provider.ErrGateway)
}

// --- HTTP ------------------------------------------------------------------

// call signs and sends one SNAP transactional request and returns the raw
// response body. externalID is echoed in the X-EXTERNAL-ID header, Midtrans'
// per-request idempotency key; callers pass a fresh unique value from
// [externalID]. It maps transport-level failures onto contract sentinels;
// response-code level errors are left to the caller via [checkCode].
func (p *Provider) call(ctx context.Context, method, path, externalID string, payload any, op string) (json.RawMessage, error) {
	body, err := marshal(payload)
	if err != nil {
		return nil, provider.NewError(Name, op, "ENCODE", err.Error(), provider.ErrInvalidRequest)
	}

	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	timestamp := time.Now().In(jakarta).Format(iso8601Layout)
	signature, err := signRequest(p.clientSecret, method, path, token, body, timestamp)
	if err != nil {
		return nil, provider.NewError(Name, op, "SIGN", err.Error(), provider.ErrGateway)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, provider.NewError(Name, op, "REQUEST", err.Error(), provider.ErrGateway)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(headerTimestamp, timestamp)
	req.Header.Set(headerSignature, signature)
	req.Header.Set(headerClientID, p.clientID)
	req.Header.Set(headerPartnerID, p.partnerID)
	req.Header.Set(headerExternalID, firstNonEmpty(externalID, externalIDFallback()))
	req.Header.Set(headerChannelID, p.channelID)
	if p.deviceID != "" {
		req.Header.Set(headerDeviceID, p.deviceID)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, provider.NewError(Name, op, "NETWORK", err.Error(), provider.ErrGateway)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, provider.NewError(Name, op, "READ", err.Error(), provider.ErrGateway)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// A stale/expired token may be the cause; drop it so the next call
		// fetches a fresh one.
		p.dropToken()
		return nil, provider.NewError(Name, op, strconv.Itoa(resp.StatusCode),
			"midtrans rejected our credentials", provider.ErrUnauthorized)
	case resp.StatusCode == http.StatusNotFound:
		return nil, provider.NewError(Name, op, strconv.Itoa(resp.StatusCode), truncate(string(raw)), provider.ErrNotFound)
	case resp.StatusCode >= http.StatusInternalServerError:
		return nil, provider.NewError(Name, op, strconv.Itoa(resp.StatusCode), truncate(string(raw)), provider.ErrGateway)
	}
	return raw, nil
}

// dropToken clears the cached access token.
func (p *Provider) dropToken() {
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()
	p.token = ""
	p.tokenExpiry = time.Time{}
}

// decode unmarshals a response body into out, wrapping JSON problems as request
// errors.
func decode(raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: cannot decode midtrans-snap response: %v", provider.ErrInvalidRequest, err)
	}
	return nil
}

// checkCode inspects the SNAP responseCode: anything starting with "200" is a
// success, everything else becomes a [*provider.Error].
func checkCode(op string, r apiResponse) error {
	if strings.HasPrefix(strings.TrimSpace(r.ResponseCode), "200") {
		return nil
	}
	message := firstNonEmpty(r.ResponseMessage, r.ResponseCode)
	return provider.NewError(Name, op, r.ResponseCode, message, classifyCode(r.ResponseCode))
}

// --- helpers ---------------------------------------------------------------

// expiry returns the absolute expiry instant, applying the default and clamping
// to Midtrans' 180-day maximum.
func (p *Provider) expiry(requested time.Duration) time.Time {
	d := requested
	if d <= 0 {
		d = p.defaultExpiry
	}
	if d > maxExpiry {
		d = maxExpiry
	}
	return time.Now().Add(d)
}

// remember stores correlation data, pruning expired records opportunistically.
func (p *Provider) remember(reference string, rec *record) {
	if strings.TrimSpace(reference) == "" || rec == nil {
		return
	}
	rec.createdAt = time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records[reference] = rec
	p.pruneLocked()
}

// lookup finds a record by merchant reference or by provider reference.
func (p *Provider) lookup(reference, providerRef string) *record {
	reference = strings.TrimSpace(reference)
	providerRef = strings.TrimSpace(providerRef)

	p.mu.RLock()
	defer p.mu.RUnlock()
	if reference != "" {
		if rec, ok := p.records[reference]; ok {
			return rec
		}
	}
	if providerRef != "" {
		for _, rec := range p.records {
			if rec.providerRef == providerRef || rec.paymentCode == providerRef {
				return rec
			}
		}
	}
	return nil
}

// pruneLocked drops records older than recordTTL. It runs at most once per
// pruneEvery to keep the cost off the hot path.
func (p *Provider) pruneLocked() {
	now := time.Now()
	if now.Sub(p.lastPrune) < pruneEvery {
		return
	}
	p.lastPrune = now
	for reference, rec := range p.records {
		if now.Sub(rec.createdAt) > recordTTL {
			delete(p.records, reference)
		}
	}
}

// referenceID returns the merchant reference of a create request.
func referenceID(req *provider.CreatePaymentRequest) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.ReferenceID)
}

// descriptionOr returns the payment description, falling back to the merchant
// reference so gateway fields that require text are never empty.
func descriptionOr(req *provider.CreatePaymentRequest) string {
	if req == nil {
		return ""
	}
	if d := strings.TrimSpace(req.Description); d != "" {
		return d
	}
	return req.ReferenceID
}

// customerDetailsFrom splits a unified customer into Midtrans' first/last name
// fields. It returns nil when there is nothing to send.
func customerDetailsFrom(c provider.Customer) *ewalletCustomerDetails {
	first, last := splitName(c.Name)
	if first == "" && c.Email == "" && c.Phone == "" {
		return nil
	}
	return &ewalletCustomerDetails{
		FirstName: first,
		LastName:  last,
		Email:     strings.TrimSpace(c.Email),
		Phone:     strings.TrimSpace(c.Phone),
	}
}

// externalIDFallback produces an X-EXTERNAL-ID when the caller passes none.
func externalIDFallback() string {
	return externalID()
}
