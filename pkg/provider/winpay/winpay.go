// Package winpay implements the [provider.Provider] contract for Winpay
// (winpay.id), an Indonesian payment gateway (PJP) that exposes Bank
// Indonesia's SNAP API standard.
//
// It covers the three products documented at
// https://docs.winpay.id/en/payments/snap-api:
//
//   - Virtual Account — one-off (c), open-recurring (o) and close-recurring (r)
//     VAs. Channels: BRI, BNI, MANDIRI, PERMATA, BSI, MUAMALAT, BCA, CIMB,
//     SINARMAS, BNC, MAYBANK, DANAMON, JATIM, and the BCAPC/MANDIRIPC payment
//     codes.
//   - QRIS — dynamic and static merchant-presented QR.
//   - eWallet — Speedcash (SC), OVO, DANA and ShopeePay (SPAY).
//
// # Authentication
//
// SNAP's "Asymmetric Without Get Token" scheme is used. Every request carries an
// RSA-SHA256 (PKCS#1 v1.5) signature in the X-SIGNATURE header, computed over
//
//	HTTPMethod:EndpointUrl:Lowercase(HexEncode(SHA-256(minify(body)))):X-TIMESTAMP
//
// Inbound callbacks are authenticated with Winpay's public key using the same
// formula; see [Provider.VerifyWebhookSignature].
//
// # Configuration
//
// Keys are read from OPENPAY_PROVIDER_WINPAY_<KEY>:
//
//	partner_id     X-PARTNER-ID issued by Winpay (required)
//	private_key    our RSA private key for signing requests (required): a PEM
//	               block or single-line base64 DER, PKCS#1 or PKCS#8
//	public_key     Winpay's RSA public key for verifying callbacks (optional;
//	               when empty, callback signatures are not checked)
//	base_url       https://sandbox-snap.winpay.id (default) or https://snap.winpay.id
//	channel_id     CHANNEL-ID header value (default "WEB")
//	callback_url   public URL of this platform's callback endpoint, used as the
//	               eWallet PAY_NOTIFY url, e.g. https://openpay.example/webhooks/winpay
//	callback_path  path used when verifying callback signatures; defaults to the
//	               path the callback actually arrived on
//	default_expiry payment lifetime as a Go duration (default "24h", max 90 days)
//	timeout        per-request HTTP timeout (default "30s")
//
// # Correlation cache
//
// Winpay addresses its status, inquiry and cancel endpoints by the contractId
// (and, for Virtual Accounts, the VA number) it returned at creation time, plus
// the channel. The unified [provider.InquiryRequest] carries none of those, so
// the adapter keeps a short-lived in-process cache filled by CreatePayment. A
// restart loses it; a production deployment would persist these identifiers
// alongside the transaction.
package winpay

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
// URLs ("/webhooks/winpay").
const Name = "winpay"

// Callback event types produced by [Provider.ParseWebhook]. They also select
// the acknowledgement body in [Provider.WebhookResponse].
const (
	EventVAPayment      = "transfer-va.payment"
	EventQRISPayment    = "qris.payment"
	EventEWalletPayment = "ewallet.payment"
)

// SNAP acknowledgement codes Winpay expects from the merchant callback URL.
const (
	responseVAPayment      = "2002500"
	responseQRISPayment    = "2005200"
	responseEWalletPayment = "2005600"
)

// SNAP header names.
const (
	headerTimestamp  = "X-TIMESTAMP"
	headerSignature  = "X-SIGNATURE"
	headerPartnerID  = "X-PARTNER-ID"
	headerExternalID = "X-EXTERNAL-ID"
	headerChannelID  = "CHANNEL-ID"
)

// Endpoint paths, relative to the SNAP base URL.
const (
	pathCreateVA  = "/v1.0/transfer-va/create-va"
	pathInquiryVA = "/v1.0/transfer-va/inquiry-va"
	pathStatusVA  = "/v1.0/transfer-va/status"
	pathDeleteVA  = "/v1.0/transfer-va/delete-va"

	pathQRISGenerate = "/v1.0/qr/qr-mpm-generate"
	pathQRISQuery    = "/v1.0/qr/qr-mpm-query"
	pathQRISCancel   = "/v1.0/qr/qr-mpm-cancel"

	pathDebitPayment = "/v1.0/debit/payment-host-to-host"
	pathDebitStatus  = "/v1.0/debit/status"
	pathDebitCancel  = "/v1.0/debit/cancel"
)

// Defaults.
const (
	defaultBaseURL   = "https://sandbox-snap.winpay.id"
	defaultChannelID = "WEB"
	defaultTimeout   = 30 * time.Second
	defaultExpiry    = 24 * time.Hour
	maxExpiry        = 90 * 24 * time.Hour
	maxResponseBytes = 1 << 20 // 1 MiB

	// Cache retention for correlation records.
	recordTTL  = 24 * time.Hour
	pruneEvery = time.Minute
)

// Operation names used in [provider.Error].
const (
	opCreate  = "CreatePayment"
	opInquiry = "Inquiry"
	opCancel  = "Cancel"
	opWebhook = "ParseWebhook"
	opVerify  = "VerifyWebhookSignature"
)

// Provider is the Winpay adapter. It is safe for concurrent use.
type Provider struct {
	partnerID     string
	privateKey    *rsa.PrivateKey
	publicKey     *rsa.PublicKey
	baseURL       string
	channelID     string
	callbackURL   string
	callbackPath  string
	defaultExpiry time.Duration
	client        *http.Client

	mu        sync.RWMutex
	records   map[string]*record
	lastPrune time.Time
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

// New builds a Winpay adapter from configuration. See the package documentation
// for the recognized keys.
func New(cfg provider.Config) (*Provider, error) {
	partnerID, err := cfg.Required("partner_id")
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
		partnerID:     partnerID,
		privateKey:    privateKey,
		baseURL:       strings.TrimRight(cfg.Get("base_url", defaultBaseURL), "/"),
		channelID:     cfg.Get("channel_id", defaultChannelID),
		callbackURL:   strings.TrimSpace(cfg.Get("callback_url", "")),
		callbackPath:  strings.TrimSpace(cfg.Get("callback_path", "")),
		defaultExpiry: defaultExpiry,
		client:        &http.Client{Timeout: defaultTimeout},
		records:       make(map[string]*record),
	}

	if raw := cfg.Get("public_key", ""); raw != "" {
		publicKey, err := parsePublicKey(raw)
		if err != nil {
			return nil, err
		}
		p.publicKey = publicKey
	}
	if raw := cfg.Get("timeout", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("winpay: invalid timeout %q: %w", raw, err)
		}
		p.client.Timeout = d
	}
	if raw := cfg.Get("default_expiry", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("winpay: invalid default_expiry %q: %w", raw, err)
		}
		p.defaultExpiry = d
	}
	return p, nil
}

// init registers the factory so the host application can build Winpay from
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
	// Sorted so the list is stable across calls (it is exposed through the API).
	channels := make([]string, 0, len(vaChannels)+len(ewalletChannels)+1)
	for code := range vaChannels {
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
	if err := validateCreate(req); err != nil {
		return nil, err
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
			fmt.Sprintf("winpay does not support method %q", req.Method), provider.ErrUnsupportedOperation)
	}
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
			fmt.Sprintf("winpay only settles IDR, got %q", req.Currency), provider.ErrInvalidRequest)
	}
	return nil
}

// createVA issues a virtual account via POST /v1.0/transfer-va/create-va.
func (p *Provider) createVA(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	if channel == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"virtual accounts require a channel code (e.g. BCA, BNI, MANDIRI)", provider.ErrInvalidRequest)
	}

	trxType := strings.ToLower(strings.TrimSpace(req.Metadata["va_trx_type"]))
	if trxType == "" {
		trxType = vaTypeOneOff
	}
	switch trxType {
	case vaTypeOneOff, vaTypeOpen, vaTypeClose:
	default:
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			`metadata "va_trx_type" must be one of "c" (one-off), "o" (open) or "r" (close)`, provider.ErrInvalidRequest)
	}

	payload := createVARequest{
		VirtualAccountName:    accountName(req.Customer.Name, req.ReferenceID),
		TrxID:                 req.ReferenceID,
		VirtualAccountTrxType: trxType,
		AdditionalInfo:        vaAdditional{Channel: channel},
	}
	switch trxType {
	case vaTypeOneOff, vaTypeClose:
		// Amount and expiry are mandatory, and the VA is fixed-value.
		payload.TotalAmount = &amountPayload{Value: formatAmount(req.Amount), Currency: string(provider.IDR)}
		payload.ExpiredDate = p.expiry(req.Expiry).In(jakarta).Format(iso8601Layout)
	default:
		// Open-recurring VAs let the payer choose the amount, so the account
		// number must be supplied up front and there is no expiry.
		customerNo := firstNonEmpty(req.Metadata["customer_no"], req.Customer.Reference)
		if customerNo == "" {
			return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
				`open-recurring VAs require metadata "customer_no"`, provider.ErrInvalidRequest)
		}
		payload.CustomerNo = customerNo
	}

	raw, err := p.call(ctx, http.MethodPost, pathCreateVA, payload, opCreate)
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
		providerRef: data.AdditionalInfo.ContractID,
		paymentCode: vaNo,
		channel:     firstNonEmpty(data.AdditionalInfo.Channel, channel),
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
	return &provider.CreatePaymentResponse{
		ProviderReference: data.AdditionalInfo.ContractID,
		ReferenceID:       reference,
		Status:            provider.StatusPending,
		Amount:            amount,
		PaymentCode:       vaNo,
		Actions:           actions,
		ExpiresAt:         parseTime(data.ExpiredDate),
		Raw:               raw,
	}, nil
}

// createQRIS generates a merchant-presented QR via
// POST /v1.0/qr/qr-mpm-generate.
func (p *Provider) createQRIS(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	if channel := strings.ToUpper(strings.TrimSpace(req.Channel)); channel != "" && channel != "QRIS" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			fmt.Sprintf("qris channel must be \"QRIS\", got %q", channel), provider.ErrInvalidRequest)
	}

	isStatic := strings.EqualFold(strings.TrimSpace(req.Metadata["is_static"]), "true")
	payload := createQRISRequest{
		PartnerReferenceNo: req.ReferenceID,
		Amount:             amountPayload{Value: formatAmount(req.Amount), Currency: string(provider.IDR)},
		TerminalID:         strings.TrimSpace(req.Metadata["terminal_id"]),
		AdditionalInfo:     qrisAdditional{IsStatic: isStatic},
	}
	if !isStatic {
		// Required for dynamic QR codes only.
		payload.ValidityPeriod = p.expiry(req.Expiry).In(jakarta).Format(iso8601Layout)
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

	qrContent := ""
	if out.QRContent != nil {
		qrContent = strings.TrimSpace(*out.QRContent)
	}
	reference := firstNonEmpty(out.PartnerReferenceNo, req.ReferenceID)

	p.remember(reference, &record{
		providerRef: out.AdditionalInfo.ContractID,
		channel:     "QRIS",
		method:      provider.MethodQRIS,
		trxID:       reference,
		amount:      req.Amount,
	})

	var actions []provider.PaymentAction
	if qrContent != "" {
		actions = append(actions, provider.PaymentAction{
			Type: provider.ActionQRCode, Value: qrContent, Description: "QRIS payload to render",
		})
	}
	if out.QRURL != "" {
		actions = append(actions, provider.PaymentAction{
			Type: provider.ActionURL, Value: out.QRURL, Description: "Hosted QR image",
		})
	}
	return &provider.CreatePaymentResponse{
		ProviderReference: out.AdditionalInfo.ContractID,
		ReferenceID:       reference,
		Status:            provider.StatusPending,
		Amount:            req.Amount,
		PaymentCode:       firstNonEmpty(qrContent, out.QRURL),
		Actions:           actions,
		ExpiresAt:         parseTime(out.AdditionalInfo.ExpiredAt),
		Raw:               raw,
	}, nil
}

// createEWallet starts an e-wallet payment via
// POST /v1.0/debit/payment-host-to-host.
func (p *Provider) createEWallet(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	channel := strings.ToUpper(strings.TrimSpace(req.Channel))
	if channel == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"e-wallets require a channel code (SC, OVO, DANA, SPAY)", provider.ErrInvalidRequest)
	}
	phone := strings.TrimSpace(req.Customer.Phone)
	if phone == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"e-wallets require customer.phone", provider.ErrInvalidRequest)
	}
	callback := firstNonEmpty(req.CallbackURL, p.callbackURL)
	if callback == "" {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
			"e-wallets require a callback URL: set the provider callback_url or pass callback_url per payment",
			provider.ErrInvalidRequest)
	}
	returnURL := firstNonEmpty(req.Metadata["return_url"], callback)

	payload := createEWalletRequest{
		PartnerReferenceNo: req.ReferenceID,
		Amount:             amountPayload{Value: formatAmount(req.Amount), Currency: string(provider.IDR)},
		URLParam: []urlParam{
			{URL: callback, Type: "PAY_NOTIFY", IsDeeplink: "N"},
			{URL: returnURL, Type: "PAY_RETURN", IsDeeplink: "N"},
		},
		ValidUpTo: p.expiry(req.Expiry).In(jakarta).Format(iso8601Layout),
		AdditionalInfo: ewalletAdditional{
			Channel:       channel,
			CustomerPhone: phone,
			CustomerName:  accountName(req.Customer.Name, req.ReferenceID),
		},
	}

	raw, err := p.call(ctx, http.MethodPost, pathDebitPayment, payload, opCreate)
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
	p.remember(reference, &record{
		providerRef: out.AdditionalInfo.ContractID,
		channel:     firstNonEmpty(out.AdditionalInfo.Channel, channel),
		method:      provider.MethodEWallet,
		trxID:       reference,
		amount:      req.Amount,
	})

	var actions []provider.PaymentAction
	if out.AppRedirectURL != "" {
		actions = append(actions, provider.PaymentAction{
			Type: provider.ActionDeeplink, Value: out.AppRedirectURL, Description: "Open the e-wallet app",
		})
	}
	if out.WebRedirectURL != "" {
		actions = append(actions, provider.PaymentAction{
			Type: provider.ActionURL, Value: out.WebRedirectURL, Description: "E-wallet payment page",
		})
	}
	return &provider.CreatePaymentResponse{
		ProviderReference: out.AdditionalInfo.ContractID,
		ReferenceID:       reference,
		Status:            provider.StatusPending,
		Amount:            req.Amount,
		Actions:           actions,
		ExpiresAt:         parseTime(out.AdditionalInfo.ExpiredTime),
		Raw:               raw,
	}, nil
}

// --- inquiry ---------------------------------------------------------------

// Inquiry implements [provider.Provider]. It relies on the correlation cache
// filled by CreatePayment; see the package documentation.
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
			"transaction is not known to this instance: Winpay inquiry needs the contractId and channel captured at creation",
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

	if vaNo != "" {
		payload := vaStatusRequest{
			VirtualAccountNo: vaNo,
			AdditionalInfo:   vaRequestAdditional{ContractID: rec.providerRef, Channel: rec.channel, TrxID: trxID},
		}
		raw, err := p.call(ctx, http.MethodPost, pathStatusVA, payload, opInquiry)
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
		status := mapVAStatus(out.VirtualAccountData.PaymentFlagStatus)
		return &provider.InquiryResponse{
			ReferenceID:       trxID,
			ProviderReference: rec.providerRef,
			Status:            status,
			Amount:            amountOr(out.VirtualAccountData.TotalAmount, rec.amount),
			PaymentCode:       firstNonEmpty(strings.TrimSpace(out.VirtualAccountData.VirtualAccountNo), vaNo),
			PaidAt:            paidAtIfPaid(status, parseTime(out.VirtualAccountData.TransactionDate)),
			Raw:               raw,
		}, nil
	}

	// Without a cached VA number we can only ask whether the account still
	// exists; the amount and status flags are not returned by this endpoint.
	payload := vaInquiryRequest{TrxID: trxID, AdditionalInfo: vaRequestAdditional{ContractID: rec.providerRef}}
	raw, err := p.call(ctx, http.MethodPost, pathInquiryVA, payload, opInquiry)
	if err != nil {
		return nil, err
	}
	var out inquiryVAResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if err := checkCode(opInquiry, out.apiResponse); err != nil {
		return nil, err
	}
	return &provider.InquiryResponse{
		ReferenceID:       trxID,
		ProviderReference: rec.providerRef,
		Status:            provider.StatusPending,
		Amount:            amountOr(out.VirtualAccountData.TotalAmount, rec.amount),
		PaymentCode:       strings.TrimSpace(out.VirtualAccountData.VirtualAccountNo),
		ExpiresAt:         parseTime(out.VirtualAccountData.ExpiredDate),
		Raw:               raw,
	}, nil
}

func (p *Provider) inquireQRIS(ctx context.Context, req *provider.InquiryRequest, rec *record) (*provider.InquiryResponse, error) {
	trxID := firstNonEmpty(rec.trxID, req.ReferenceID)
	payload := qrisQueryRequest{
		OriginalPartnerReferenceNo: trxID,
		ServiceCode:                serviceCodeQRIS,
		AdditionalInfo:             contractAdditional{ContractID: rec.providerRef},
	}
	raw, err := p.call(ctx, http.MethodPost, pathQRISQuery, payload, opInquiry)
	if err != nil {
		return nil, err
	}
	var out qrisQueryResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if err := checkCode(opInquiry, out.apiResponse); err != nil {
		return nil, err
	}
	status := mapTransactionStatus(out.LatestTransactionStatus, provider.StatusUnknown)
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
		AdditionalInfo:             ewalletRequestAdditional{ContractID: rec.providerRef, Channel: rec.channel},
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
	// eWallet reports "07" as "unpaid", not "not found".
	status := mapTransactionStatus(out.LatestTransactionStatus, provider.StatusPending)
	return &provider.InquiryResponse{
		ReferenceID:       trxID,
		ProviderReference: rec.providerRef,
		Status:            status,
		Amount:            rec.amount,
		Raw:               raw,
	}, nil
}

// --- cancel ----------------------------------------------------------------

// Cancel implements [provider.Provider]. It cancels a not-yet-settled payment;
// Winpay rejects cancellation of paid transactions.
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
			"transaction is not known to this instance: Winpay cancel needs the contractId captured at creation",
			provider.ErrNotFound)
	}

	reason := firstNonEmpty(req.Reason, "Cancelled by merchant")
	trxID := firstNonEmpty(rec.trxID, req.ReferenceID)

	switch rec.method {
	case provider.MethodVirtualAccount:
		return p.cancelVA(ctx, rec, trxID, reason)
	case provider.MethodQRIS:
		return p.cancelQRIS(ctx, rec, trxID, reason)
	case provider.MethodEWallet:
		return p.cancelEWallet(ctx, rec, trxID, reason)
	default:
		return nil, provider.NewError(Name, opCancel, "UNSUPPORTED_METHOD",
			fmt.Sprintf("cannot cancel method %q", rec.method), provider.ErrUnsupportedOperation)
	}
}

func (p *Provider) cancelVA(ctx context.Context, rec *record, trxID, reason string) (*provider.CancelResponse, error) {
	vaNo := strings.TrimSpace(rec.paymentCode)
	if vaNo == "" {
		// Resolve the VA number through the inquiry endpoint before deleting.
		payload := vaInquiryRequest{TrxID: trxID, AdditionalInfo: vaRequestAdditional{ContractID: rec.providerRef}}
		raw, err := p.call(ctx, http.MethodPost, pathInquiryVA, payload, opCancel)
		if err != nil {
			return nil, err
		}
		var out inquiryVAResponse
		if err := decode(raw, &out); err != nil {
			return nil, provider.WrapError(Name, opCancel, err)
		}
		if err := checkCode(opCancel, out.apiResponse); err != nil {
			return nil, err
		}
		vaNo = strings.TrimSpace(out.VirtualAccountData.VirtualAccountNo)
		if vaNo == "" {
			return nil, provider.NewError(Name, opCancel, "NOT_FOUND",
				"cannot resolve the virtual account number", provider.ErrNotFound)
		}
	}

	payload := vaDeleteRequest{
		VirtualAccountNo: vaNo,
		TrxID:            trxID,
		AdditionalInfo:   vaRequestAdditional{ContractID: rec.providerRef, Channel: rec.channel},
	}
	raw, err := p.call(ctx, http.MethodDelete, pathDeleteVA, payload, opCancel)
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
		ProviderReference: rec.providerRef,
		Status:            provider.StatusCancelled,
		Raw:               raw,
	}, nil
}

func (p *Provider) cancelQRIS(ctx context.Context, rec *record, trxID, reason string) (*provider.CancelResponse, error) {
	payload := qrisCancelRequest{
		OriginalPartnerReferenceNo: trxID,
		Reason:                     reason,
		AdditionalInfo:             contractAdditional{ContractID: rec.providerRef},
	}
	raw, err := p.call(ctx, http.MethodPost, pathQRISCancel, payload, opCancel)
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

func (p *Provider) cancelEWallet(ctx context.Context, rec *record, trxID, reason string) (*provider.CancelResponse, error) {
	payload := ewalletCancelRequest{
		OriginalPartnerReferenceNo: trxID,
		Reason:                     reason,
		AdditionalInfo:             ewalletRequestAdditional{ContractID: rec.providerRef, Channel: rec.channel},
	}
	raw, err := p.call(ctx, http.MethodPost, pathDebitCancel, payload, opCancel)
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
		ProviderReference: rec.providerRef,
		Status:            provider.StatusCancelled,
		Raw:               raw,
	}, nil
}

// --- callbacks -------------------------------------------------------------

// ParseWebhook implements [provider.Provider]. It recognizes the three
// documented callback shapes and maps them onto a unified [provider.WebhookEvent].
func (p *Provider) ParseWebhook(ctx context.Context, req *provider.WebhookRequest) (*provider.WebhookEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opWebhook, err)
	}
	if req == nil || len(req.Body) == 0 {
		return nil, provider.NewError(Name, opWebhook, "EMPTY", "empty callback body", provider.ErrInvalidRequest)
	}
	var payload callbackPayload
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
			"cannot parse callback body: "+err.Error(), provider.ErrInvalidRequest)
	}

	evt := &provider.WebhookEvent{Raw: append(json.RawMessage(nil), req.Body...)}

	switch {
	case payload.PaidAmount != nil || strings.TrimSpace(payload.VirtualAccountNo) != "":
		// Virtual Account payment notification: always a settled payment.
		evt.EventType = EventVAPayment
		evt.ReferenceID = strings.TrimSpace(payload.TrxID)
		evt.ProviderReference = payload.AdditionalInfo.ContractID
		evt.Status = provider.StatusPaid
		if payload.PaidAmount != nil {
			evt.Amount, _ = parseAmount(payload.PaidAmount.Value)
		}
		evt.PaidAt = parseTime(payload.TrxDateTime)

	case strings.TrimSpace(payload.LatestTransactionStatus) != "":
		evt.ReferenceID = strings.TrimSpace(payload.OriginalPartnerReferenceNo)
		evt.ProviderReference = payload.AdditionalInfo.ContractID
		if payload.Amount != nil {
			evt.Amount, _ = parseAmount(payload.Amount.Value)
		}
		if channelMethod(payload.AdditionalInfo.Channel) == provider.MethodEWallet {
			evt.EventType = EventEWalletPayment
			evt.Status = mapTransactionStatus(payload.LatestTransactionStatus, provider.StatusPending)
		} else {
			evt.EventType = EventQRISPayment
			evt.Status = mapTransactionStatus(payload.LatestTransactionStatus, provider.StatusUnknown)
		}
		evt.PaidAt = paidAtIfPaid(evt.Status, parseTime(firstNonEmpty(payload.PaidTime, payload.TrxDateTime)))

	default:
		return nil, provider.NewError(Name, opWebhook, "UNRECOGNIZED",
			"payload does not match a known Winpay callback", provider.ErrInvalidRequest)
	}

	// Fill in whichever identifier the callback omitted, using the cache.
	if evt.ReferenceID == "" || evt.ProviderReference == "" {
		if rec := p.lookup(evt.ReferenceID, evt.ProviderReference); rec != nil {
			if evt.ReferenceID == "" {
				evt.ReferenceID = rec.trxID
			}
			if evt.ProviderReference == "" {
				evt.ProviderReference = rec.providerRef
			}
		}
	}
	if !evt.Correlatable() {
		return nil, provider.NewError(Name, opWebhook, "NO_REFERENCE",
			"callback carries no transaction reference", provider.ErrInvalidRequest)
	}
	return evt, nil
}

// VerifyWebhookSignature implements [provider.SignatureVerifier] using Winpay's
// public key. When no public key is configured the check is skipped, matching
// the behaviour of the reference mock provider.
func (p *Provider) VerifyWebhookSignature(req *provider.WebhookRequest) error {
	if req == nil {
		return provider.NewError(Name, opVerify, "EMPTY", "empty callback", provider.ErrInvalidSignature)
	}
	if p.publicKey == nil {
		return nil
	}
	signature := req.Header(headerSignature)
	timestamp := req.Header(headerTimestamp)
	if signature == "" || timestamp == "" {
		return provider.NewError(Name, opVerify, "MISSING_HEADERS",
			"callback is missing "+headerSignature+" or "+headerTimestamp, provider.ErrInvalidSignature)
	}
	endpoint := firstNonEmpty(p.callbackPath, req.Path)
	if endpoint == "" {
		return provider.NewError(Name, opVerify, "MISSING_PATH",
			"cannot determine the callback path used in the signature", provider.ErrInvalidSignature)
	}
	body, err := minify(req.Body)
	if err != nil {
		return provider.NewError(Name, opVerify, "BAD_JSON", err.Error(), provider.ErrInvalidSignature)
	}
	if err := verifySignature(p.publicKey, http.MethodPost, endpoint, body, timestamp, signature); err != nil {
		return provider.NewError(Name, opVerify, "BAD_SIGNATURE", err.Error(), provider.ErrInvalidSignature)
	}
	return nil
}

// WebhookResponse implements [provider.WebhookResponder], returning the SNAP
// acknowledgement Winpay expects (otherwise it retries the callback).
func (p *Provider) WebhookResponse(evt *provider.WebhookEvent) (int, []byte) {
	code := responseVAPayment
	if evt != nil {
		switch evt.EventType {
		case EventQRISPayment:
			code = responseQRISPayment
		case EventEWalletPayment:
			code = responseEWalletPayment
		}
	}
	body, err := json.Marshal(apiResponse{ResponseCode: code, ResponseMessage: "Successful"})
	if err != nil {
		return http.StatusOK, []byte(fmt.Sprintf(`{"responseCode":%q,"responseMessage":"Successful"}`, code))
	}
	return http.StatusOK, body
}

// --- HTTP ------------------------------------------------------------------

// call signs and sends one SNAP request, returning the raw response body. It
// maps transport-level failures onto contract sentinels; response-code level
// errors are left to the caller via [checkCode].
func (p *Provider) call(ctx context.Context, method, path string, payload any, op string) (json.RawMessage, error) {
	body, err := marshal(payload)
	if err != nil {
		return nil, provider.NewError(Name, op, "ENCODE", err.Error(), provider.ErrInvalidRequest)
	}
	timestamp := time.Now().In(jakarta).Format(iso8601Layout)
	signature, err := signRequest(p.privateKey, method, path, body, timestamp)
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
	req.Header.Set(headerPartnerID, p.partnerID)
	req.Header.Set(headerExternalID, externalID())
	req.Header.Set(headerChannelID, p.channelID)

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
		return nil, provider.NewError(Name, op, strconv.Itoa(resp.StatusCode),
			"winpay rejected our credentials", provider.ErrUnauthorized)
	case resp.StatusCode == http.StatusNotFound:
		return nil, provider.NewError(Name, op, strconv.Itoa(resp.StatusCode), truncate(string(raw)), provider.ErrNotFound)
	case resp.StatusCode >= http.StatusInternalServerError:
		return nil, provider.NewError(Name, op, strconv.Itoa(resp.StatusCode), truncate(string(raw)), provider.ErrGateway)
	}
	return raw, nil
}

// decode unmarshals a response body into out, wrapping JSON problems as request
// errors.
func decode(raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: cannot decode winpay response: %v", provider.ErrInvalidRequest, err)
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
// to Winpay's 3 month maximum.
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

// lookup finds a record by merchant reference or by Winpay contractId.
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
