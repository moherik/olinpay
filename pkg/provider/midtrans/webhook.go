package midtrans

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"strings"

	"github.com/moherik/openpay.id/pkg/provider"
)

// Callback event types produced by [Provider.ParseWebhook]. The legacy Core API
// posts a single notification shape for every product; the event name records
// which product the payment_type denotes. Midtrans acknowledges a notification
// on any 2xx response, so no event-specific acknowledgement body is required.
const (
	EventVAPayment      = "bank_transfer.payment"
	EventEWalletPayment = "ewallet.payment"
	EventQRISPayment    = "qris.payment"
	EventRetailPayment  = "cstore.payment"
)

// ParseWebhook implements [provider.Provider]. Midtrans' Core API posts one JSON
// body for every product: transaction_status carries the lifecycle state and
// payment_type identifies the product. Signature verification is performed by
// [Provider.VerifyWebhookSignature], which the webhook engine calls before this
// method.
func (p *Provider) ParseWebhook(ctx context.Context, req *provider.WebhookRequest) (*provider.WebhookEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opWebhook, err)
	}
	if req == nil || len(req.Body) == 0 {
		return nil, provider.NewError(Name, opWebhook, "EMPTY", "empty callback body", provider.ErrInvalidRequest)
	}

	var n notification
	if err := json.Unmarshal(req.Body, &n); err != nil {
		return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
			"cannot parse callback body: "+err.Error(), provider.ErrInvalidRequest)
	}

	status := mapTransactionStatus(n.TransactionStatus, n.FraudStatus)
	evt := &provider.WebhookEvent{
		ReferenceID:       strings.TrimSpace(n.OrderID),
		ProviderReference: strings.TrimSpace(n.TransactionID),
		Status:            status,
		Amount:            amountOr(n.GrossAmount, 0),
		PaidAt:            paidAtIfPaid(status, parseTime(firstNonEmpty(n.SettlementTime, n.TransactionTime))),
		EventType:         eventTypeFor(n.PaymentType),
		Raw:               append(json.RawMessage(nil), req.Body...),
	}
	if !evt.Correlatable() {
		return nil, provider.NewError(Name, opWebhook, "NOT_CORRELATABLE",
			"notification carries neither an order_id nor a transaction_id", provider.ErrInvalidRequest)
	}
	return evt, nil
}

// VerifyWebhookSignature implements [provider.SignatureVerifier]. Midtrans
// authenticates a Core API notification with the signature_key field:
//
//	SHA512(order_id + status_code + gross_amount + server_key)
//
// The adapter recomputes the digest and compares it in constant time. When
// server_key is not configured the callback is rejected rather than trusted.
func (p *Provider) VerifyWebhookSignature(req *provider.WebhookRequest) error {
	if req == nil {
		return provider.NewError(Name, opVerify, "EMPTY", "empty callback", provider.ErrInvalidSignature)
	}
	if len(req.Body) == 0 {
		return provider.NewError(Name, opVerify, "EMPTY", "empty callback body", provider.ErrInvalidSignature)
	}
	if strings.TrimSpace(p.serverKey) == "" {
		return provider.NewError(Name, opVerify, "NO_SERVER_KEY",
			"server_key is not configured; refusing to accept unauthenticated callbacks",
			provider.ErrInvalidSignature)
	}

	var n notification
	if err := json.Unmarshal(req.Body, &n); err != nil {
		return provider.NewError(Name, opVerify, "BAD_JSON",
			"cannot parse callback body: "+err.Error(), provider.ErrInvalidSignature)
	}

	signature := strings.ToLower(strings.TrimSpace(n.SignatureKey))
	if signature == "" {
		return provider.NewError(Name, opVerify, "MISSING_SIGNATURE",
			"callback is missing signature_key", provider.ErrInvalidSignature)
	}

	want := notificationSignature(n.OrderID, n.StatusCode, n.GrossAmount, p.serverKey)
	if subtle.ConstantTimeCompare([]byte(want), []byte(signature)) != 1 {
		return provider.NewError(Name, opVerify, "BAD_SIGNATURE",
			"signature_key does not match the callback body", provider.ErrInvalidSignature)
	}
	return nil
}
