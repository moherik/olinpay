// Package doku implements the [provider.Provider] contract for DOKU
// (doku.com), an Indonesian payment gateway (PJP) that exposes Bank Indonesia's
// SNAP API standard.
//
// It covers the three products documented at
// https://developers.doku.com/accept-payments/direct-api/snap:
//
//   - Virtual Account — one-off (C), open (O) and bill-variable (V) VAs across
//     the major Indonesian banks (BCA, BRI, BNI, BNC, Mandiri, Permata, CIMB,
//     BSI, Danamon, Maybank, Sinarmas, BTN, BJB).
//   - QRIS — merchant-presented QR.
//   - eWallet — DANA and ShopeePay via the direct-debit host-to-host flow.
//
// # Authentication
//
// DOKU's default "Symmetric Signature with Get Token" scheme is used. First a
// B2B access token is obtained from /authorization/v1/access-token/b2b, signing
// with RSA-SHA256 (PKCS#1 v1.5) over "clientID|timestamp". Every transactional
// request then carries an HMAC-SHA512 signature in the X-SIGNATURE header,
// computed with the merchant's client_secret over
//
//	HTTPMethod:EndpointUrl:AccessToken:Lowercase(HexEncode(SHA-256(minify(body)))):X-TIMESTAMP
//
// and an Authorization: Bearer <token> header. The access token is cached and
// refreshed shortly before its 900s expiry, and once more on a 401.
//
// Inbound callbacks are authenticated with HMAC-SHA512 over the same shape;
// see [Provider.VerifyWebhookSignature].
//
// # Configuration
//
// Keys are read from OPENPAY_PROVIDER_DOKU_<KEY>:
//
//	client_id        DOKU client ID issued during onboarding (required)
//	client_secret    DOKU client secret, the HMAC key (required)
//	private_key      our RSA private key for signing the get-token request
//	                 (required): a PEM block or single-line base64 DER
//	base_url         https://api-sandbox.doku.com (default) or https://api.doku.com
//	partner_service_id 8-digit left-space-padded BIN / company code, required
//	                 for Virtual Account
//	merchant_id     DOKU merchant ID, required for QRIS
//	terminal_id      DOKU terminal ID, required for QRIS
//	postal_code      payer postal code, required for QRIS
//	callback_url     public URL of this platform's callback endpoint, used as
//	                 the e-wallet PAY_RETURN / PAY_NOTIFY url
//	channel_id       CHANNEL-ID header value (default "H2H")
//	va_path          override the per-bank create-va / delete-va path
//	default_expiry   payment lifetime as a Go duration (default "24h")
//	timeout          per-request HTTP timeout (default "30s")
//
// # Correlation cache
//
// DOKU addresses its status and delete endpoints by identifiers (the VA number,
// the original reference numbers) that the unified contract does not carry
// between calls. Like the Winpay adapter, this adapter keeps a short-lived
// in-process cache filled by CreatePayment. A restart loses it; a production
// deployment would persist these identifiers alongside the transaction.
//
// # Sandbox status
//
// Only the sandbox base URL is the default. Channel availability is
// account-specific and has NOT been verified against the live DOKU sandbox.
package doku

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// Name is the provider identifier used in configuration, routing and callback
// URLs ("/webhooks/doku").
const Name = "doku"

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
)

// Callback event types produced by [Provider.ParseWebhook]. They also select
// the acknowledgement body in [Provider.WebhookResponse].
const (
	EventVAPayment      = "transfer-va.payment"
	EventEWalletPayment = "ewallet.payment"
	EventQRISPayment    = "qris.payment"
)

// SNAP acknowledgement codes DOKU expects from the merchant callback URL.
const (
	responseVAPayment      = "2002500"
	responseEWalletPayment = "2005600"
	responseQRISPayment    = "2005600"
)

// DOKU endpoint paths, relative to the SNAP base URL.
const (
	pathGetToken = "/authorization/v1/access-token/b2b"

	pathVAStatus      = "/orders/v1.0/transfer-va/status"
	pathDebitStatus   = "/orders/v1.0/debit/status"
	pathQRISGenerate  = "/snap-adapter/b2b/v1.0/qr/qr-mpm-generate"
	pathQRISQuery     = "/snap-adapter/b2b/v1.0/qr/qr-mpm-query"
	pathQRISExpire    = "/snap-adapter/b2b/v1.0/qr/qr-expire"
	pathDebitPayment1 = "/direct-debit/core/v1/debit/payment-host-to-host"
	pathDebitPayment2 = "/direct-debit/core/v2/debit/payment-host-to-host"

	// VA notification path (DOKU -> merchant).
	pathVANotify = "/v1/transfer-va/payment"
	// Direct-debit notification path (DOKU -> merchant).
	pathDDNotify = "/v1.0/debit/notify"
)

// Defaults.
const (
	defaultBaseURL   = "https://api-sandbox.doku.com"
	defaultChannelID = "H2H"
	defaultTimeout   = 30 * time.Second
	defaultExpiry    = 24 * time.Hour
	maxExpiry        = 90 * 24 * time.Hour
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

// Provider implements [provider.Provider] for DOKU.
type Provider struct {
	clientID     string
	clientSecret string
	privateKey   *rsa.PrivateKey

	baseURL       string
	partnerSvcID  string
	merchantID    string
	terminalID    string
	postalCode    string
	callbackURL   string
	channelID     string
	vaPath        string
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
	amount      provider.Amount
	createdAt   time.Time
}

// New builds a DOKU adapter from configuration. See the package documentation
// for the recognized keys.
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
		partnerSvcID:  strings.TrimRight(cfg.Get("partner_service_id", ""), " \t\r\n"),
		merchantID:    strings.TrimSpace(cfg.Get("merchant_id", "")),
		terminalID:    strings.TrimSpace(cfg.Get("terminal_id", "")),
		postalCode:    strings.TrimSpace(cfg.Get("postal_code", "")),
		callbackURL:   strings.TrimSpace(cfg.Get("callback_url", "")),
		channelID:     cfg.Get("channel_id", defaultChannelID),
		vaPath:        strings.TrimSpace(cfg.Get("va_path", "")),
		defaultExpiry: defaultExpiry,
		client:        provider.NewHTTPClient(Name, defaultTimeout),
		records:       make(map[string]*record),
	}

	if raw := cfg.Get("timeout", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("doku: invalid timeout %q: %w", raw, err)
		}
		p.client.Timeout = d
	}
	if raw := cfg.Get("default_expiry", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("doku: invalid default_expiry %q: %w", raw, err)
		}
		p.defaultExpiry = d
	}
	return p, nil
}

// init registers the factory so the host application can build DOKU from
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

	switch req.Method {
	case provider.MethodVirtualAccount:
		return p.createVA(ctx, req)
	case provider.MethodQRIS:
		return p.createQRIS(ctx, req)
	case provider.MethodEWallet:
		return p.createEWallet(ctx, req)
	default:
		return nil, provider.NewError(Name, opCreate, "UNSUPPORTED_METHOD",
			fmt.Sprintf("unsupported method %q", req.Method), provider.ErrUnsupportedOperation)
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

	trxType := strings.ToUpper(strings.TrimSpace(req.Metadata["va_trx_type"]))
	if trxType == "" {
		trxType = vaTypeClosed
	}
	switch trxType {
	case vaTypeClosed, vaTypeOpen, vaTypeBill:
	default:
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			`metadata "va_trx_type" must be one of "C", "O" or "V"`, provider.ErrInvalidRequest)
	}

	payload := createVARequest{
		PartnerServiceID:      p.partnerSvcID,
		VirtualAccountName:    accountName(req.Customer.Name, req.ReferenceID),
		TrxID:                 req.ReferenceID,
		VirtualAccountTrxType: trxType,
		TotalAmount:           &amountPayload{Value: formatAmount(req.Amount), Currency: string(provider.IDR)},
		AdditionalInfo:        vaAdditional{Channel: bank.channel},
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
	// MGPC flow: when the caller supplies a customer number we forward it and
	// let DOKU derive the VA number.
	if customerNo := strings.TrimSpace(req.Metadata["customer_no"]); customerNo != "" {
		payload.CustomerNo = customerNo
		payload.VirtualAccountNo = p.partnerSvcID + customerNo
	}

	path := firstNonEmpty(p.vaPath, bank.path)
	raw, err := p.call(ctx, http.MethodPost, path, payload, opCreate)
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
	amount := req.Amount
	if data.TotalAmount != nil {
		if parsed, err := parseAmount(data.TotalAmount.Value); err == nil && parsed > 0 {
			amount = parsed
		}
	}
	reference := firstNonEmpty(data.TrxID, req.ReferenceID)

	p.remember(reference, &record{
		providerRef: vaNo,
		paymentCode: vaNo,
		channel:     channel,
		method:      provider.MethodVirtualAccount,
		trxID:       reference,
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
	if page := strings.TrimSpace(data.AdditionalInfo.HowToPayPage); page != "" {
		actions = append(actions, provider.PaymentAction{
			Type:        provider.ActionURL,
			Value:       page,
			Description: "How-to-pay page",
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
			fmt.Sprintf("qris channel must be \"QRIS\", got %q", channel), provider.ErrInvalidRequest)
	}
	if p.merchantID == "" || p.terminalID == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"qris requires the merchant_id and terminal_id config", provider.ErrInvalidRequest)
	}
	if p.postalCode == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"qris requires the postal_code config", provider.ErrInvalidRequest)
	}

	payload := createQRISRequest{
		PartnerReferenceNo: req.ReferenceID,
		Amount:             &amountPayload{Value: formatAmount(req.Amount), Currency: string(provider.IDR)},
		MerchantID:         p.merchantID,
		TerminalID:         p.terminalID,
		AdditionalInfo:     qrisAdditional{PostalCode: p.postalCode, FeeType: 1},
	}
	if expiry := p.expiry(req.Expiry); !expiry.IsZero() {
		payload.ValidityPeriod = expiry.In(jakarta).Format(iso8601Layout)
	}

	raw, err := p.call(ctx, http.MethodPost, pathQRISGenerate, payload, opCreate)
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

func (p *Provider) createEWallet(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	if channel == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"e-wallets require a channel code (DANA, SHOPEEPAY)", provider.ErrInvalidRequest)
	}
	ew, ok := ewalletChannels[channel]
	if !ok {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("unsupported e-wallet channel %q", channel), provider.ErrInvalidRequest)
	}
	callback := firstNonEmpty(req.Metadata["return_url"], req.ReturnURL, req.SuccessReturnURL, req.FailureReturnURL, req.CallbackURL, p.callbackURL)
	if callback == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"e-wallets require a callback URL: set the provider callback_url or pass callback_url per payment",
			provider.ErrInvalidRequest)
	}

	payload := createEWalletRequest{
		PartnerReferenceNo: req.ReferenceID,
		Amount:             &amountPayload{Value: formatAmount(req.Amount), Currency: string(provider.IDR)},
		AdditionalInfo:     ewalletAdditional{Channel: ew.channel},
	}
	if expiry := p.expiry(req.Expiry); !expiry.IsZero() {
		payload.ValidUpTo = expiry.In(jakarta).Format(iso8601Layout)
	}
	if ew.version == 1 {
		payload.URLParam = &ewalletURLParam{URL: callback, Type: "PAY_RETURN", IsDeepLink: "N"}
	} else {
		payload.URLParams = []ewalletURLParam{{URL: callback, Type: "PAY_RETURN", IsDeepLink: "N"}}
	}

	path := pathDebitPayment1
	if ew.version == 2 {
		path = pathDebitPayment2
	}
	raw, err := p.call(ctx, http.MethodPost, path, payload, opCreate)
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
		amount:      req.Amount,
	})

	var actions []provider.PaymentAction
	if url := strings.TrimSpace(out.WebRedirectURL); url != "" {
		actions = append(actions, provider.PaymentAction{
			Type:        provider.ActionURL,
			Value:       url,
			Description: "Complete the payment in the e-wallet app",
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
			"transaction is not known to this instance: DOKU status needs the identifiers captured at creation",
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
		CustomerNo:       strings.TrimPrefix(vaNo, p.partnerSvcID),
		VirtualAccountNo: vaNo,
	}
	if payload.PartnerServiceID == "" {
		payload.PartnerServiceID = firstNonEmpty(p.partnerSvcID, "        ")
	}

	raw, err := p.call(ctx, http.MethodPost, pathVAStatus, payload, opInquiry)
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
		ReferenceID:       trxID,
		ProviderReference: vaNo,
		Status:            status,
		Amount:            amountOr(out.VirtualAccountData.PaidAmount, rec.amount),
		PaymentCode:       firstNonEmpty(strings.TrimSpace(out.VirtualAccountData.VirtualAccountNo), vaNo),
		PaidAt:            paidAtIfPaid(status, parseTime(out.VirtualAccountData.TransactionDate)),
		Raw:               raw,
	}, nil
}

func (p *Provider) inquireQRIS(ctx context.Context, req *provider.InquiryRequest, rec *record) (*provider.InquiryResponse, error) {
	trxID := firstNonEmpty(rec.trxID, req.ReferenceID)
	if p.merchantID == "" {
		return nil, provider.NewError(Name, opInquiry, "INVALID_REQUEST",
			"qris requires the merchant_id config", provider.ErrInvalidRequest)
	}
	payload := queryQRISRequest{
		OriginalPartnerReferenceNo: trxID,
		ServiceCode:                serviceCodeQRIS,
		MerchantID:                 p.merchantID,
	}
	raw, err := p.call(ctx, http.MethodPost, pathQRISQuery, payload, opInquiry)
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
		ReferenceID:       trxID,
		ProviderReference: rec.providerRef,
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
		ServiceCode:                serviceCodeDebit,
	}
	if rec.providerRef != "" {
		payload.OriginalReferenceNo = rec.providerRef
	}
	raw, err := p.call(ctx, http.MethodPost, pathDebitStatus, payload, opInquiry)
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
		ReferenceID:       trxID,
		ProviderReference: firstNonEmpty(out.OriginalReferenceNo, rec.providerRef),
		Status:            status,
		Amount:            rec.amount,
		Raw:               raw,
	}, nil
}

// --- cancel ----------------------------------------------------------------

// Cancel implements [provider.Provider].
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
			"transaction is not known to this instance: DOKU cancel needs the identifiers captured at creation",
			provider.ErrNotFound)
	}

	switch rec.method {
	case provider.MethodVirtualAccount:
		return p.cancelVA(ctx, rec)
	case provider.MethodQRIS:
		return p.cancelQRIS(ctx, rec)
	case provider.MethodEWallet:
		return nil, provider.NewError(Name, opCancel, "UNSUPPORTED_METHOD",
			"DOKU does not expose a cancel endpoint for e-wallet host-to-host payments",
			provider.ErrUnsupportedOperation)
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

	channel := strings.ToUpper(strings.TrimSpace(rec.channel))
	bank, ok := vaBanks[channel]
	if !ok {
		return nil, provider.NewError(Name, opCancel, "INVALID_REQUEST",
			fmt.Sprintf("unsupported virtual account channel %q", channel), provider.ErrInvalidRequest)
	}
	base := firstNonEmpty(p.vaPath, bank.path)
	path := strings.Replace(base, "create-va", "delete-va", 1)
	if path == base {
		// A custom va_path that does not end in create-va: replace the suffix.
		if i := strings.LastIndex(base, "/"); i >= 0 {
			path = base[:i+1] + "delete-va"
		}
	}

	payload := deleteVARequest{
		PartnerServiceID: p.partnerSvcID,
		CustomerNo:       strings.TrimPrefix(vaNo, p.partnerSvcID),
		VirtualAccountNo: vaNo,
		TrxID:            trxID,
		AdditionalInfo:   vaAdditional{Channel: bank.channel},
	}
	raw, err := p.call(ctx, http.MethodDelete, path, payload, opCancel)
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

func (p *Provider) cancelQRIS(ctx context.Context, rec *record) (*provider.CancelResponse, error) {
	trxID := firstNonEmpty(rec.trxID, "")
	if p.merchantID == "" {
		return nil, provider.NewError(Name, opCancel, "INVALID_REQUEST",
			"qris requires the merchant_id config", provider.ErrInvalidRequest)
	}
	payload := cancelQRISRequest{
		PartnerReferenceNo: trxID,
		ReferenceNo:        rec.providerRef,
		MerchantID:         p.merchantID,
	}
	raw, err := p.call(ctx, http.MethodPost, pathQRISExpire, payload, opCancel)
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
		ProviderReference: rec.providerRef,
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
	signature, err := signGetToken(p.privateKey, p.clientID, timestamp)
	if err != nil {
		return "", 0, provider.NewError(Name, opToken, "SIGN", err.Error(), provider.ErrGateway)
	}
	body, err := marshal(getTokenRequest{GrantType: "client_credentials"})
	if err != nil {
		return "", 0, provider.NewError(Name, opToken, "ENCODE", err.Error(), provider.ErrInvalidRequest)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+pathGetToken, bytes.NewReader(body))
	if err != nil {
		return "", 0, provider.NewError(Name, opToken, "REQUEST", err.Error(), provider.ErrGateway)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CLIENT-KEY", p.clientID)
	req.Header.Set("X-TIMESTAMP", timestamp)
	req.Header.Set("X-SIGNATURE", signature)

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
			"doku rejected our credentials", provider.ErrUnauthorized)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return "", 0, provider.NewError(Name, opToken, strconv.Itoa(resp.StatusCode), truncate(string(raw)), provider.ErrGateway)
	}

	var out getTokenResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", 0, provider.NewError(Name, opToken, "BAD_JSON", "cannot decode get-token response: "+err.Error(), provider.ErrInvalidRequest)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.ResponseCode), "200") {
		return "", 0, provider.NewError(Name, opToken, out.ResponseCode, firstNonEmpty(out.ResponseMessage, "get-token failed"), classifyCode(out.ResponseCode))
	}
	if token := strings.TrimSpace(out.AccessToken); token != "" {
		if out.ExpiresIn <= 0 {
			out.ExpiresIn = 900
		}
		return token, out.ExpiresIn, nil
	}
	return "", 0, provider.NewError(Name, opToken, "NO_TOKEN", "get-token response carried no access token", provider.ErrGateway)
}

// --- HTTP ------------------------------------------------------------------

// call signs and sends one SNAP transactional request, returning the raw
// response body. It maps transport-level failures onto contract sentinels;
// response-code level errors are left to the caller via [checkCode].
func (p *Provider) call(ctx context.Context, method, path string, payload any, op string) (json.RawMessage, error) {
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
	req.Header.Set(headerPartnerID, p.clientID)
	req.Header.Set(headerExternalID, externalID())
	req.Header.Set(headerChannelID, p.channelID)
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
			"doku rejected our credentials", provider.ErrUnauthorized)
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
		return fmt.Errorf("%w: cannot decode doku response: %v", provider.ErrInvalidRequest, err)
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
// to DOKU's 90-day maximum.
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

// amountOr prefers the amount reported by the gateway, falling back to the
// amount captured at creation time.
func amountOr(payload *amountPayload, fallback provider.Amount) provider.Amount {
	if payload != nil {
		if parsed, err := parseAmount(payload.Value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
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
			if rec.providerRef == providerRef {
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
