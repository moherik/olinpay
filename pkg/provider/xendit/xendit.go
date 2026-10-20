// Package xendit implements the [provider.Provider] contract for Xendit
// (xendit.co), a payment gateway operating across South-East Asia.
//
// It uses Xendit's Payments API v3 (https://docs.xendit.co/apidocs/create-payment-request),
// a single "payment request" endpoint that covers one-off and reusable payment
// flows. A payment request selects a concrete instrument with a channel_code:
//
//   - Virtual Account — BCA_VIRTUAL_ACCOUNT, BNI_VIRTUAL_ACCOUNT,
//     BRI_VIRTUAL_ACCOUNT, MANDIRI_VIRTUAL_ACCOUNT, PERMATA_VIRTUAL_ACCOUNT,
//     CIMB_VIRTUAL_ACCOUNT, BSI_VIRTUAL_ACCOUNT, BNC_VIRTUAL_ACCOUNT,
//     MUAMALAT_VIRTUAL_ACCOUNT and BJB_VIRTUAL_ACCOUNT.
//   - QRIS — the dynamic merchant-presented QR (channel_code "QRIS"), returned
//     as a QR_STRING action and as the payment code.
//   - eWallet — OVO, DANA, SHOPEEPAY, LINKAJA, JENIUSPAY, ASTRAPAY, GOPAY and
//     NEXCASH, returned as redirect/deeplink actions.
//   - Retail outlet — Alfamart and Indomaret (reusable payment code).
//   - Pay later — Akulaku, Kredivo, Atome and Indodana.
//   - Card — via a saved Xendit payment token, passed in
//     metadata["payment_token_id"] (or metadata["card_token"]).
//
// # Authentication
//
// Every request uses HTTP Basic Auth with the secret key as the username and an
// empty password: Authorization: Basic base64("<secret_key>:"). The v3 API
// additionally requires an api-version header, sent on every request.
//
// # Callbacks
//
// Xendit authenticates callbacks with a shared token in the x-callback-token
// header, which must match the verification token configured in the Xendit
// webhook settings (callback_token here). The adapter compares it in constant
// time and refuses to accept callbacks when the token is not configured.
//
// Xendit delivers an event envelope ({"event": "payment.capture", "data": {...}})
// covering payments, payment requests and refunds. The adapter normalizes the
// data status (and the refund events by name) onto a [provider.Status].
//
// # Configuration
//
// Keys are read from OPENPAY_PROVIDER_XENDIT_<KEY>:
//
//	secret_key      Xendit secret key (required): "xnd_development_..." for
//	                test mode, "xnd_production_..." for live
//	base_url        https://api.xendit.co (default); test and live use the same
//	                host, selected by the key
//	api_version     value of the api-version header (default "2024-11-11")
//	callback_token  verification token for inbound callbacks; when empty,
//	                callbacks are rejected
//	return_url      where the payer returns after redirect-based flows
//	                (e-wallets, cards); per-request success/failure URLs or
//	                metadata["success_return_url"] take precedence
//	default_expiry  default lifetime for virtual accounts and QRIS as a Go
//	                duration (default "24h")
//	timeout         per-request HTTP timeout (default "30s")
//
// # Idempotency
//
// Xendit keys a payment request by its own generated payment_request_id, so
// Inquiry and Cancel address the request by the value returned as
// ProviderReference; no correlation cache is needed. The merchant ReferenceID is
// passed through as reference_id.
package xendit

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
// URLs ("/webhooks/xendit").
const Name = "xendit"

// Endpoint paths, relative to the API base URL.
const (
	pathPaymentRequests = "/v3/payment_requests"
)

// HTTP header names.
const (
	headerAuthorization = "Authorization"
	headerAPIVersion    = "api-version"
)

// Defaults.
const (
	defaultBaseURL    = "https://api.xendit.co"
	defaultAPIVersion = "2024-11-11"
	defaultTimeout    = 30 * time.Second
	defaultExpiry     = 24 * time.Hour
	countryID         = "ID"
	maxResponseBytes  = 1 << 20 // 1 MiB
)

// Provider is the Xendit adapter. It is safe for concurrent use.
type Provider struct {
	secretKey     string
	baseURL       string
	apiVersion    string
	callbackToken string
	returnURL     string
	defaultExpiry time.Duration
	client        *http.Client
}

// New builds a Xendit adapter from configuration. See the package documentation
// for the recognized keys.
func New(cfg provider.Config) (*Provider, error) {
	secretKey, err := cfg.Required("secret_key")
	if err != nil {
		return nil, err
	}

	p := &Provider{
		secretKey:     secretKey,
		baseURL:       strings.TrimRight(cfg.Get("base_url", defaultBaseURL), "/"),
		apiVersion:    cfg.Get("api_version", defaultAPIVersion),
		callbackToken: strings.TrimSpace(cfg.Get("callback_token", "")),
		returnURL:     strings.TrimSpace(cfg.Get("return_url", "")),
		defaultExpiry: defaultExpiry,
		client:        provider.NewHTTPClient(Name, defaultTimeout),
	}
	if raw := cfg.Get("timeout", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("xendit: invalid timeout %q: %w", raw, err)
		}
		p.client.Timeout = d
	}
	if raw := cfg.Get("default_expiry", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("xendit: invalid default_expiry %q: %w", raw, err)
		}
		p.defaultExpiry = d
	}
	return p, nil
}

// init registers the factory so the host application can build Xendit from
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
	channels := make([]string, 0, len(vaChannels)+len(ewalletChannels)+len(retailChannels)+len(paylaterChannels)+2)
	for code := range vaChannels {
		channels = append(channels, code)
	}
	channels = append(channels, channelQRIS)
	for code := range ewalletChannels {
		channels = append(channels, code)
	}
	for code := range retailChannels {
		channels = append(channels, code)
	}
	for code := range paylaterChannels {
		channels = append(channels, code)
	}
	channels = append(channels, channelCard)
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
	payload, err := p.buildPaymentRequest(req)
	if err != nil {
		return nil, err
	}

	raw, err := p.call(ctx, http.MethodPost, pathPaymentRequests, payload, opCreate)
	if err != nil {
		return nil, err
	}
	var out paymentRequestResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCreate, err)
	}
	return createResponse(req, &out, raw), nil
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
			fmt.Sprintf("xendit only settles IDR, got %q", req.Currency), provider.ErrInvalidRequest)
	}
	return nil
}

// buildPaymentRequest assembles the Payments API body for the requested method.
func (p *Provider) buildPaymentRequest(req *provider.CreatePaymentRequest) (*paymentRequest, error) {
	pr := &paymentRequest{
		ReferenceID:   strings.TrimSpace(req.ReferenceID),
		Country:       countryID,
		Currency:      string(provider.IDR),
		RequestAmount: req.Amount,
		Description:   descriptionOr(req),
		Metadata:      metadataForXendit(req.Metadata),
	}

	switch req.Method {
	case provider.MethodVirtualAccount:
		channel, ok := vaChannels[strings.ToUpper(strings.TrimSpace(req.Channel))]
		if !ok {
			return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
				fmt.Sprintf("unsupported virtual account channel %q", req.Channel), provider.ErrInvalidRequest)
		}
		pr.Type = typePay
		pr.ChannelCode = channel
		props := map[string]any{}
		if exp := p.expiry(req.Expiry); exp > 0 {
			props["expires_at"] = time.Now().Add(exp).UTC().Format(time.RFC3339)
		}
		if name := customerName(req.Customer); name != "" {
			props["display_name"] = name
		}
		pr.ChannelProperties = props

	case provider.MethodQRIS:
		pr.Type = typePay
		pr.ChannelCode = channelQRIS
		if exp := p.expiry(req.Expiry); exp > 0 {
			pr.ChannelProperties = map[string]any{
				"expires_at": time.Now().Add(exp).UTC().Format(time.RFC3339),
			}
		}

	case provider.MethodEWallet:
		channel, ok := ewalletChannels[strings.ToUpper(strings.TrimSpace(req.Channel))]
		if !ok {
			return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
				fmt.Sprintf("unsupported e-wallet channel %q", req.Channel), provider.ErrInvalidRequest)
		}
		pr.Type = typePay
		pr.ChannelCode = channel
		pr.ChannelProperties = p.redirectProperties(req)

	case provider.MethodRetailOutlet:
		channel, ok := retailChannels[strings.ToUpper(strings.TrimSpace(req.Channel))]
		if !ok {
			return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
				fmt.Sprintf("unsupported retail outlet channel %q (ALFAMART, INDOMARET)", req.Channel), provider.ErrInvalidRequest)
		}
		pr.Type = typeReusablePaymentCode
		pr.ChannelCode = channel
		props := map[string]any{}
		if name := customerName(req.Customer); name != "" {
			props["customer_name"] = name
		}
		pr.ChannelProperties = props

	case provider.MethodPaylater:
		channel, ok := paylaterChannels[strings.ToUpper(strings.TrimSpace(req.Channel))]
		if !ok {
			return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
				fmt.Sprintf("unsupported pay later channel %q", req.Channel), provider.ErrInvalidRequest)
		}
		pr.Type = typePay
		pr.ChannelCode = channel
		pr.ChannelProperties = p.redirectProperties(req)

	case provider.MethodCard:
		token := firstNonEmpty(req.Metadata["payment_token_id"], req.Metadata["card_token"])
		if token == "" {
			return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST",
				`card payments require metadata "payment_token_id" from Xendit's tokenization flow`, provider.ErrInvalidRequest)
		}
		pr.Type = typePay
		pr.PaymentTokenID = token
		pr.ChannelProperties = p.redirectProperties(req)

	default:
		return nil, provider.NewError(Name, opCreate, "UNSUPPORTED_METHOD",
			fmt.Sprintf("xendit does not support method %q", req.Method), provider.ErrUnsupportedOperation)
	}

	merged, err := mergeChannelProperties(pr.ChannelProperties, req.Metadata["channel_properties"])
	if err != nil {
		return nil, provider.NewError(Name, opCreate, "INVALID_REQUEST", err.Error(), provider.ErrInvalidRequest)
	}
	pr.ChannelProperties = merged
	return pr, nil
}

// redirectProperties builds the success/failure return URLs used by the
// redirect-based flows.
func (p *Provider) redirectProperties(req *provider.CreatePaymentRequest) map[string]any {
	success := firstNonEmpty(req.Metadata["success_return_url"], req.SuccessReturnURL, req.Metadata["return_url"], req.ReturnURL, p.returnURL)
	failure := firstNonEmpty(req.Metadata["failure_return_url"], req.FailureReturnURL, req.Metadata["return_url"], req.ReturnURL, p.returnURL)
	var props map[string]any
	if success != "" || failure != "" {
		props = map[string]any{}
		if success != "" {
			props["success_return_url"] = success
		}
		if failure != "" {
			props["failure_return_url"] = failure
		}
	}
	return props
}

// createResponse maps a payment request onto the unified create response.
func createResponse(req *provider.CreatePaymentRequest, out *paymentRequestResponse, raw json.RawMessage) *provider.CreatePaymentResponse {
	actions, code := mapActions(out.Actions)
	if code == "" {
		code = channelPropertyString(out.ChannelProperties, "virtual_account_number", "payment_code", "qr_string", "account_number")
	}
	return &provider.CreatePaymentResponse{
		ProviderReference: strings.TrimSpace(out.PaymentRequestID),
		ReferenceID:       firstNonEmpty(out.ReferenceID, req.ReferenceID),
		Status:            mapStatus(out.Status),
		Amount:            amountOr(out.RequestAmount, req.Amount),
		PaymentCode:       code,
		Actions:           actions,
		ExpiresAt:         expiresAt(out.ChannelProperties),
		Raw:               raw,
	}
}

// --- inquiry ---------------------------------------------------------------

// Inquiry implements [provider.Provider]. It fetches the payment request by its
// Xendit id (ProviderReference), since the v3 API does not expose lookup by the
// merchant reference.
func (p *Provider) Inquiry(ctx context.Context, req *provider.InquiryRequest) (*provider.InquiryResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}
	if req == nil {
		return nil, provider.NewError(Name, opInquiry, "INVALID_REQUEST",
			"provider_reference (payment_request_id) is required", provider.ErrInvalidRequest)
	}
	id := addressID(req.ReferenceID, req.ProviderReference)
	if id == "" {
		return nil, provider.NewError(Name, opInquiry, "INVALID_REQUEST",
			"xendit inquiry requires provider_reference (the payment_request_id)", provider.ErrInvalidRequest)
	}

	raw, err := p.call(ctx, http.MethodGet, pathPaymentRequests+"/"+url.PathEscape(id), nil, opInquiry)
	if err != nil {
		return nil, err
	}
	var out paymentRequestResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opInquiry, err)
	}

	status := mapStatus(out.Status)
	_, code := mapActions(out.Actions)
	if code == "" {
		code = channelPropertyString(out.ChannelProperties, "virtual_account_number", "payment_code", "qr_string", "account_number")
	}
	var captured *time.Time
	if out.LatestPayment != nil {
		captured = lastCaptureTime(out.LatestPayment.Captures)
	}

	return &provider.InquiryResponse{
		ReferenceID:       firstNonEmpty(out.ReferenceID, req.ReferenceID),
		ProviderReference: firstNonEmpty(out.PaymentRequestID, req.ProviderReference),
		Status:            status,
		Amount:            amountOr(out.RequestAmount, 0),
		PaymentCode:       code,
		PaidAt:            paidAtIfPaid(status, firstNonEmptyTime(captured, parseTime(out.Updated))),
		ExpiresAt:         expiresAt(out.ChannelProperties),
		Raw:               raw,
	}, nil
}

// --- cancel ----------------------------------------------------------------

// Cancel implements [provider.Provider]. The v3 API has a single cancel
// endpoint; a request for "expire" is handled as a cancel, since Xendit exposes
// no forced-expiry operation.
func (p *Provider) Cancel(ctx context.Context, req *provider.CancelRequest) (*provider.CancelResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}
	if req == nil {
		return nil, provider.NewError(Name, opCancel, "INVALID_REQUEST",
			"provider_reference (payment_request_id) is required", provider.ErrInvalidRequest)
	}
	id := addressID(req.ReferenceID, req.ProviderReference)
	if id == "" {
		return nil, provider.NewError(Name, opCancel, "INVALID_REQUEST",
			"xendit cancel requires provider_reference (the payment_request_id)", provider.ErrInvalidRequest)
	}

	raw, err := p.call(ctx, http.MethodPost, pathPaymentRequests+"/"+url.PathEscape(id)+"/cancel", nil, opCancel)
	if err != nil {
		return nil, err
	}
	var out paymentRequestResponse
	if err := decode(raw, &out); err != nil {
		return nil, provider.WrapError(Name, opCancel, err)
	}

	status := mapStatus(out.Status)
	if status == provider.StatusUnknown {
		status = provider.StatusCancelled
	}
	return &provider.CancelResponse{
		ReferenceID:       firstNonEmpty(out.ReferenceID, req.ReferenceID),
		ProviderReference: firstNonEmpty(out.PaymentRequestID, req.ProviderReference),
		Status:            status,
		Raw:               raw,
	}, nil
}

// --- HTTP ------------------------------------------------------------------

// call sends one Payments API request (authenticated with Basic Auth and the
// api-version header) and returns the raw response body. Transport-level and
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
	if p.apiVersion != "" {
		req.Header.Set(headerAPIVersion, p.apiVersion)
	}
	req.Header.Set(headerAuthorization, "Basic "+base64.StdEncoding.EncodeToString([]byte(p.secretKey+":")))

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

// apiError turns an unsuccessful Xendit response into a [*provider.Error],
// keeping the gateway's error_code and message.
func apiError(op string, httpStatus int, raw []byte) error {
	var envelope errorResponse
	_ = json.Unmarshal(raw, &envelope)

	code := firstNonEmpty(envelope.ErrorCode, strconv.Itoa(httpStatus))
	message := firstNonEmpty(envelope.Message, truncate(string(raw)))
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
		return fmt.Errorf("%w: cannot decode xendit response: %v", provider.ErrInvalidRequest, err)
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

// addressID picks the identifier Xendit should address the payment request by.
// The v3 API only supports lookup by its own generated id, so ProviderReference
// (payment_request_id) wins; a reference_id is accepted only when it already
// looks like a payment request id.
func addressID(reference, providerReference string) string {
	if id := strings.TrimSpace(providerReference); id != "" {
		return id
	}
	ref := strings.TrimSpace(reference)
	if strings.HasPrefix(ref, "pr-") {
		return ref
	}
	return ""
}
