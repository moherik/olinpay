// Package mock implements a fully self-contained, in-memory payment gateway
// that satisfies the [provider.Provider] contract.
//
// It exists to serve two purposes:
//
//   - as a reference implementation showing exactly how to map a gateway onto
//     the unified contract, including signature verification and webhook
//     parsing;
//   - as a deterministically testable provider for local development, since it
//     requires no network access or credentials.
//
// Real integrations (Midtrans, Xendit, Duitku, OY!, ...) should follow the same
// shape: a struct holding config + an *http.Client, a constructor, a Factory
// registered in init(), and pure mapping functions.
package mock

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/moherik/openpay.id/pkg/provider"
)

// Name is the provider identifier used in configuration and routing.
const Name = "mock"

// SignatureHeader is the header carrying the HMAC signature on callbacks that
// the mock gateway "sends" and that [Provider.VerifyWebhookSignature] verifies.
const SignatureHeader = "X-Mock-Signature"

// Provider is the in-memory gateway implementation.
type Provider struct {
	secret        string
	vaPrefix      string
	defaultExpiry time.Duration

	mu    sync.RWMutex
	store map[string]*record
}

// record is the internal state of a single payment.
type record struct {
	ref         string
	providerRef string
	amount      provider.Amount
	method      provider.Method
	channel     string
	status      provider.Status
	paymentCode string
	paidAt      *time.Time
	expiresAt   *time.Time
}

// New constructs the mock provider from its configuration.
//
// Recognized config keys:
//
//	secret          HMAC secret used to sign/verify callbacks (optional)
//	va_prefix       prefix for generated virtual account numbers (default "88888")
//	default_expiry  payment lifetime as a Go duration string (default "24h")
func New(cfg provider.Config) (*Provider, error) {
	p := &Provider{
		secret:        cfg.Get("secret", "mock-secret"),
		vaPrefix:      cfg.Get("va_prefix", "88888"),
		defaultExpiry: 24 * time.Hour,
		store:         make(map[string]*record),
	}
	if raw := cfg.Get("default_expiry", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("mock: invalid default_expiry %q: %w", raw, err)
		}
		p.defaultExpiry = d
	}
	return p, nil
}

// init registers the factory with the default registry so the host application
// can build the mock provider purely from configuration.
func init() {
	provider.Register(Name, func(cfg provider.Config) (provider.Provider, error) {
		return New(cfg)
	})
}

// Name implements [provider.Provider].
func (p *Provider) Name() string { return Name }

// Capabilities implements [provider.Provider].
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Methods: []provider.Method{
			provider.MethodVirtualAccount,
			provider.MethodQRIS,
			provider.MethodEWallet,
			provider.MethodRetailOutlet,
		},
		Channels:        []string{"MOCK_VA", "MOCK_QRIS", "MOCK_EWALLET", "MOCK_RETAIL"},
		SupportsInquiry: true,
		SupportsCancel:  true,
		SupportsWebhook: true,
		SupportsSandbox: true,
	}
}

// Health implements [provider.HealthChecker].
func (p *Provider) Health(context.Context) error { return nil }

// CreatePayment implements [provider.Provider].
func (p *Provider) CreatePayment(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, "CreatePayment", err)
	}
	if req == nil || req.ReferenceID == "" || req.Amount <= 0 || !req.Method.IsValid() {
		return nil, provider.NewError(Name, "CreatePayment", "INVALID_REQUEST",
			"reference_id, positive amount and a valid method are required", provider.ErrInvalidRequest)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.store[req.ReferenceID]; exists {
		return nil, provider.NewError(Name, "CreatePayment", "DUPLICATE",
			"reference already exists", provider.ErrDuplicateReference)
	}

	expiry := req.Expiry
	if expiry <= 0 {
		expiry = p.defaultExpiry
	}
	now := time.Now()
	expiresAt := now.Add(expiry)

	rec := &record{
		ref:         req.ReferenceID,
		providerRef: "MOCK-" + strings.ToUpper(uuid.NewString()[:8]),
		amount:      req.Amount,
		method:      req.Method,
		channel:     req.Channel,
		status:      provider.StatusPending,
		expiresAt:   &expiresAt,
	}
	rec.paymentCode = p.paymentCode(rec)
	p.store[req.ReferenceID] = rec

	raw, _ := json.Marshal(map[string]any{
		"id":         rec.providerRef,
		"status":     rec.status,
		"amount":     rec.amount,
		"expires_at": expiresAt.Format(time.RFC3339),
	})

	return &provider.CreatePaymentResponse{
		ProviderReference: rec.providerRef,
		ReferenceID:       rec.ref,
		Status:            rec.status,
		Amount:            rec.amount,
		PaymentCode:       rec.paymentCode,
		Actions:           p.actions(rec),
		ExpiresAt:         &expiresAt,
		Raw:               raw,
	}, nil
}

// Inquiry implements [provider.Provider].
func (p *Provider) Inquiry(ctx context.Context, req *provider.InquiryRequest) (*provider.InquiryResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, "Inquiry", err)
	}
	p.mu.Lock()
	rec := p.lookupLocked(req.ReferenceID, req.ProviderReference)
	if rec != nil {
		p.applyExpiryLocked(rec)
	}
	p.mu.Unlock()

	if rec == nil {
		return nil, provider.NewError(Name, "Inquiry", "NOT_FOUND",
			"transaction not found", provider.ErrNotFound)
	}
	return &provider.InquiryResponse{
		ReferenceID:       rec.ref,
		ProviderReference: rec.providerRef,
		Status:            rec.status,
		Amount:            rec.amount,
		PaymentCode:       rec.paymentCode,
		PaidAt:            rec.paidAt,
		ExpiresAt:         rec.expiresAt,
	}, nil
}

// Cancel implements [provider.Provider]. It covers both explicit cancel and
// forced expiry, as most gateways expose a single endpoint for both.
func (p *Provider) Cancel(ctx context.Context, req *provider.CancelRequest) (*provider.CancelResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, "Cancel", err)
	}
	p.mu.Lock()
	rec := p.lookupLocked(req.ReferenceID, req.ProviderReference)
	if rec != nil {
		switch rec.status {
		case provider.StatusPaid:
			p.mu.Unlock()
			return nil, provider.NewError(Name, "Cancel", "ALREADY_PAID",
				"paid transaction cannot be cancelled", provider.ErrInvalidRequest)
		case provider.StatusPending:
			rec.status = provider.StatusCancelled
		}
	}
	p.mu.Unlock()

	if rec == nil {
		return nil, provider.NewError(Name, "Cancel", "NOT_FOUND",
			"transaction not found", provider.ErrNotFound)
	}
	return &provider.CancelResponse{
		ReferenceID:       rec.ref,
		ProviderReference: rec.providerRef,
		Status:            rec.status,
	}, nil
}

// ParseWebhook implements [provider.Provider]. It expects a JSON body shaped
// like {"reference_id","provider_reference","status","amount","paid_at"}.
func (p *Provider) ParseWebhook(ctx context.Context, req *provider.WebhookRequest) (*provider.WebhookEvent, error) {
	if req == nil {
		return nil, provider.NewError(Name, "ParseWebhook", "EMPTY", "empty webhook", provider.ErrInvalidRequest)
	}
	var payload struct {
		ReferenceID       string          `json:"reference_id"`
		ProviderReference string          `json:"provider_reference"`
		Status            string          `json:"status"`
		Amount            provider.Amount `json:"amount"`
		PaidAt            string          `json:"paid_at"`
		EventType         string          `json:"event_type"`
	}
	if err := json.Unmarshal(req.Body, &payload); err != nil {
		return nil, provider.NewError(Name, "ParseWebhook", "BAD_JSON",
			"cannot parse webhook body", provider.ErrInvalidRequest)
	}

	status := provider.Status(strings.ToLower(payload.Status))
	if !status.IsValid() {
		return nil, provider.NewError(Name, "ParseWebhook", "BAD_STATUS",
			"unknown status "+payload.Status, provider.ErrInvalidRequest)
	}

	var paidAt *time.Time
	if payload.PaidAt != "" {
		if t, err := time.Parse(time.RFC3339, payload.PaidAt); err == nil {
			paidAt = &t
		}
	}

	evt := &provider.WebhookEvent{
		ReferenceID:       payload.ReferenceID,
		ProviderReference: payload.ProviderReference,
		Status:            status,
		Amount:            payload.Amount,
		PaidAt:            paidAt,
		EventType:         payload.EventType,
		Raw:               append(json.RawMessage(nil), req.Body...),
	}

	// Enrich from local state when the callback omits identifiers.
	if evt.ReferenceID == "" || evt.ProviderReference == "" {
		p.mu.RLock()
		rec := p.lookupLocked(evt.ReferenceID, evt.ProviderReference)
		if rec != nil {
			if evt.ReferenceID == "" {
				evt.ReferenceID = rec.ref
			}
			if evt.ProviderReference == "" {
				evt.ProviderReference = rec.providerRef
			}
		}
		p.mu.RUnlock()
	}

	if !evt.Correlatable() {
		return nil, provider.NewError(Name, "ParseWebhook", "NO_REFERENCE",
			"webhook carries no reference", provider.ErrInvalidRequest)
	}
	return evt, nil
}

// VerifyWebhookSignature implements [provider.SignatureVerifier]. It checks the
// HMAC-SHA256 of the raw body against [SignatureHeader]. When no secret is
// configured it accepts every callback.
func (p *Provider) VerifyWebhookSignature(req *provider.WebhookRequest) error {
	if p.secret == "" {
		return nil
	}
	got := req.Header(SignatureHeader)
	if got == "" {
		return provider.NewError(Name, "VerifyWebhookSignature", "MISSING_SIGNATURE",
			"missing "+SignatureHeader, provider.ErrInvalidSignature)
	}
	want := Sign(p.secret, req.Body)
	if !hmac.Equal([]byte(got), []byte(want)) {
		return provider.NewError(Name, "VerifyWebhookSignature", "BAD_SIGNATURE",
			"signature mismatch", provider.ErrInvalidSignature)
	}
	return nil
}

// Sign returns the hex HMAC-SHA256 of body using secret. It is exported so
// tests and local tooling can produce valid callbacks.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// --- internals -------------------------------------------------------------

func (p *Provider) lookupLocked(ref, providerRef string) *record {
	if ref != "" {
		if rec, ok := p.store[ref]; ok {
			return rec
		}
	}
	if providerRef != "" {
		for _, rec := range p.store {
			if rec.providerRef == providerRef {
				return rec
			}
		}
	}
	return nil
}

func (p *Provider) applyExpiryLocked(rec *record) {
	if rec.status == provider.StatusPending && rec.expiresAt != nil && time.Now().After(*rec.expiresAt) {
		rec.status = provider.StatusExpired
	}
}

// Settle marks a payment as paid. It is a backdoor used by tests and the local
// simulator to emulate a customer completing the payment.
func (p *Provider) Settle(reference string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec, ok := p.store[reference]
	if !ok {
		return fmt.Errorf("mock: unknown reference %q", reference)
	}
	now := time.Now()
	rec.status = provider.StatusPaid
	rec.paidAt = &now
	return nil
}

func (p *Provider) paymentCode(rec *record) string {
	switch rec.method {
	case provider.MethodVirtualAccount:
		return p.vaPrefix + fmt.Sprintf("%010d", time.Now().UnixNano()%1_000_000_0000)
	case provider.MethodRetailOutlet:
		return fmt.Sprintf("%012d", time.Now().UnixNano()%1_000_000_000_000)
	default:
		return ""
	}
}

func (p *Provider) actions(rec *record) []provider.PaymentAction {
	switch rec.method {
	case provider.MethodQRIS:
		return []provider.PaymentAction{{
			Type:        provider.ActionQRCode,
			Value:       "MOCKQRIS://" + rec.providerRef,
			Description: "Scan with any QRIS-enabled app",
		}}
	case provider.MethodEWallet:
		return []provider.PaymentAction{{
			Type:        provider.ActionDeeplink,
			Value:       "mockewallet://pay/" + rec.providerRef,
			Description: "Open the e-wallet app to pay",
		}}
	default:
		return nil
	}
}
