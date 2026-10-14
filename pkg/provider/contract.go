package provider

import (
	"context"
	"strings"
)

// Provider is the single interface that every payment gateway integration must
// implement. It is deliberately small: four operations covering the entire
// payment lifecycle plus inbound callbacks.
//
// Implementations MUST be safe for concurrent use.
type Provider interface {
	// Name returns the stable, unique identifier of the provider as used in
	// configuration, routing and callback URLs (e.g. "mock", "midtrans").
	// It must be lowercase and URL-safe.
	Name() string

	// Capabilities describes what this provider can do. The core engine uses
	// it for routing and for exposing feature information to callers.
	Capabilities() Capabilities

	// CreatePayment submits a new payment to the gateway and returns the
	// unified result. Implementations must map their gateway errors onto a
	// [*Error].
	CreatePayment(ctx context.Context, req *CreatePaymentRequest) (*CreatePaymentResponse, error)

	// Inquiry fetches the current state of a previously created payment.
	Inquiry(ctx context.Context, req *InquiryRequest) (*InquiryResponse, error)

	// Cancel cancels or expires a payment that has not been settled yet.
	Cancel(ctx context.Context, req *CancelRequest) (*CancelResponse, error)

	// ParseWebhook converts a raw inbound gateway callback into a normalized
	// [WebhookEvent]. It must be pure: no database access, no side effects.
	// Signature verification, when supported, should happen here or via the
	// optional [SignatureVerifier] interface.
	ParseWebhook(ctx context.Context, req *WebhookRequest) (*WebhookEvent, error)
}

// Capabilities advertises provider features to the core engine and API layer.
type Capabilities struct {
	// Methods is the set of unified methods this provider supports. An empty
	// slice means "unspecified" and is treated as supporting anything.
	Methods []Method `json:"methods,omitempty"`
	// Channels lists the concrete instruments (e.g. "BCA", "GOPAY"), if the
	// provider wants to advertise them.
	Channels []string `json:"channels,omitempty"`
	// SupportsInquiry is true when Inquiry performs a real gateway call.
	SupportsInquiry bool `json:"supports_inquiry"`
	// SupportsCancel is true when Cancel performs a real gateway call.
	SupportsCancel bool `json:"supports_cancel"`
	// SupportsWebhook is true when ParseWebhook can handle real callbacks.
	SupportsWebhook bool `json:"supports_webhook"`
	// SupportsSandbox is true when the provider is usable against a sandbox.
	SupportsSandbox bool `json:"supports_sandbox"`
}

// SupportsMethod reports whether the capability set allows the given method.
// An empty Methods slice means "supports everything".
func (c Capabilities) SupportsMethod(m Method) bool {
	if len(c.Methods) == 0 {
		return true
	}
	for _, v := range c.Methods {
		if v == m {
			return true
		}
	}
	return false
}

// SupportsChannel reports whether the capability set allows the given channel.
// An empty Channels slice means "supports everything". Matching is
// case-insensitive and ignores surrounding whitespace, because channel codes
// are gateway identifiers that callers write in whatever case they prefer
// (e.g. "bca" vs the advertised "BCA").
func (c Capabilities) SupportsChannel(ch string) bool {
	ch = strings.TrimSpace(ch)
	if ch == "" || len(c.Channels) == 0 {
		return true
	}
	for _, v := range c.Channels {
		if strings.EqualFold(strings.TrimSpace(v), ch) {
			return true
		}
	}
	return false
}

// SignatureVerifier is an OPTIONAL interface a provider MAY implement to have
// the webhook engine cryptographically authenticate inbound callbacks before
// they are parsed. When implemented, the webhook engine calls it first and
// rejects the callback if it returns a non-nil error.
type SignatureVerifier interface {
	VerifyWebhookSignature(req *WebhookRequest) error
}

// HealthChecker is an OPTIONAL interface a provider MAY implement to report its
// own readiness (e.g. credentials present, gateway reachable).
type HealthChecker interface {
	Health(ctx context.Context) error
}

// WebhookResponder is an OPTIONAL interface a provider MAY implement to control
// the exact HTTP response the webhook engine sends back to a gateway once a
// callback has been accepted.
//
// Most gateways accept any 2xx, but some (notably SNAP/Bank Indonesia gateways
// such as Winpay) keep retrying until they receive their own documented
// acknowledgement body. Implementations must return a non-zero status code.
type WebhookResponder interface {
	WebhookResponse(evt *WebhookEvent) (statusCode int, body []byte)
}
