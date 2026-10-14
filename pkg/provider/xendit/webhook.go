package xendit

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"strings"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// Operation names used in [provider.Error].
const (
	opCreate  = "CreatePayment"
	opInquiry = "Inquiry"
	opCancel  = "Cancel"
	opWebhook = "ParseWebhook"
	opVerify  = "VerifyWebhookSignature"
)

// headerCallbackToken is the header Xendit stamps on every callback.
const headerCallbackToken = "X-Callback-Token"

// VerifyWebhookSignature implements [provider.SignatureVerifier]. Xendit
// authenticates callbacks with a shared token in the x-callback-token header,
// which must match the verification token configured in the webhook settings.
//
// The token is compared in constant time. If no callback_token is configured,
// callbacks are rejected rather than trusted: refusing to authenticate is the
// safe failure mode.
func (p *Provider) VerifyWebhookSignature(req *provider.WebhookRequest) error {
	if req == nil {
		return provider.NewError(Name, opVerify, "EMPTY", "empty callback", provider.ErrInvalidSignature)
	}
	if p.callbackToken == "" {
		return provider.NewError(Name, opVerify, "NOT_CONFIGURED",
			"callback_token is not configured; refusing to accept callbacks", provider.ErrInvalidSignature)
	}
	got := strings.TrimSpace(req.Header(headerCallbackToken))
	if got == "" {
		return provider.NewError(Name, opVerify, "MISSING_TOKEN",
			"callback is missing "+headerCallbackToken, provider.ErrInvalidSignature)
	}
	if subtle.ConstantTimeCompare([]byte(p.callbackToken), []byte(got)) != 1 {
		return provider.NewError(Name, opVerify, "BAD_TOKEN",
			"callback token mismatch", provider.ErrInvalidSignature)
	}
	return nil
}

// ParseWebhook implements [provider.Provider]. Xendit wraps every event in
// `{"event": "...", "data": {...}}`; the data object carries the merchant
// reference_id, the payment_request_id, the status and the amount. It is a pure
// mapping with no I/O.
func (p *Provider) ParseWebhook(ctx context.Context, req *provider.WebhookRequest) (*provider.WebhookEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opWebhook, err)
	}
	if req == nil || len(req.Body) == 0 {
		return nil, provider.NewError(Name, opWebhook, "EMPTY", "empty callback body", provider.ErrInvalidRequest)
	}

	var envelope webhookEnvelope
	if err := json.Unmarshal(req.Body, &envelope); err != nil {
		return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
			"cannot parse callback body: "+err.Error(), provider.ErrInvalidRequest)
	}
	if strings.TrimSpace(envelope.Event) == "" || len(envelope.Data) == 0 {
		return nil, provider.NewError(Name, opWebhook, "UNRECOGNIZED",
			"callback is not a Xendit event envelope", provider.ErrInvalidRequest)
	}

	var data webhookData
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, provider.NewError(Name, opWebhook, "BAD_DATA",
			"cannot parse callback data: "+err.Error(), provider.ErrInvalidRequest)
	}

	status, err := eventStatus(envelope.Event, data.Status)
	if err != nil {
		return nil, err
	}

	amount := provider.Amount(0)
	if parsed, perr := parseAmount(data.RequestAmount); perr == nil {
		amount = parsed
	}

	evt := &provider.WebhookEvent{
		ReferenceID:       strings.TrimSpace(data.ReferenceID),
		ProviderReference: firstNonEmpty(data.PaymentRequestID, data.PaymentID),
		Status:            status,
		Amount:            amount,
		PaidAt:            paidAtIfPaid(status, firstNonEmptyTime(lastCaptureTime(data.Captures), parseTime(data.Updated))),
		EventType:         strings.TrimSpace(envelope.Event),
		Raw:               append(json.RawMessage(nil), req.Body...),
	}
	if !evt.Correlatable() {
		return nil, provider.NewError(Name, opWebhook, "NO_REFERENCE",
			"callback carries neither reference_id nor payment_request_id", provider.ErrInvalidRequest)
	}
	return evt, nil
}

// eventStatus resolves the unified status for a callback. Refund events report
// a payment-style SUCCEEDED/FAILED status, so they are mapped by event name;
// every other event maps its data status directly.
func eventStatus(event, status string) (provider.Status, error) {
	switch strings.ToLower(strings.TrimSpace(event)) {
	case "refund.succeeded":
		return provider.StatusRefunded, nil
	case "refund.failed":
		return provider.StatusFailed, nil
	}
	mapped := mapStatus(status)
	if mapped == provider.StatusUnknown {
		return provider.StatusUnknown, provider.NewError(Name, opWebhook, "UNRECOGNIZED_STATUS",
			firstNonEmpty(status, event), provider.ErrInvalidRequest)
	}
	return mapped, nil
}

// firstNonEmptyTime returns the first non-nil time.
func firstNonEmptyTime(times ...*time.Time) *time.Time {
	for _, t := range times {
		if t != nil {
			return t
		}
	}
	return nil
}
