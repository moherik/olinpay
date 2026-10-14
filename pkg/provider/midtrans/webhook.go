package midtrans

import (
	"context"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"strings"

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

// signatureKey computes the SHA-512 hex digest Midtrans uses to authenticate
// notifications: SHA512(order_id + status_code + gross_amount + server_key).
//
// Crucially, gross_amount is hashed exactly as Midtrans sent it (e.g.
// "145000.00"), not as a parsed integer.
func signatureKey(serverKey, orderID, statusCode, grossAmount string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + serverKey))
	return hex.EncodeToString(sum[:])
}

// VerifyWebhookSignature implements [provider.SignatureVerifier]. It recomputes
// Midtrans' signature_key and compares it in constant time. Midtrans always
// signs its notifications, so a missing or mismatched signature is rejected.
func (p *Provider) VerifyWebhookSignature(req *provider.WebhookRequest) error {
	if req == nil || len(req.Body) == 0 {
		return provider.NewError(Name, opVerify, "EMPTY", "empty callback", provider.ErrInvalidSignature)
	}
	var n transactionResponse
	if err := json.Unmarshal(req.Body, &n); err != nil {
		return provider.NewError(Name, opVerify, "BAD_JSON",
			"cannot parse callback body: "+err.Error(), provider.ErrInvalidSignature)
	}
	if strings.TrimSpace(n.SignatureKey) == "" {
		return provider.NewError(Name, opVerify, "MISSING_SIGNATURE",
			"callback carries no signature_key", provider.ErrInvalidSignature)
	}
	if n.OrderID == "" || n.StatusCode == "" || n.GrossAmount == "" {
		return provider.NewError(Name, opVerify, "MISSING_FIELDS",
			"callback is missing order_id, status_code or gross_amount", provider.ErrInvalidSignature)
	}

	want := signatureKey(p.serverKey, n.OrderID, n.StatusCode, n.GrossAmount)
	got := strings.ToLower(strings.TrimSpace(n.SignatureKey))
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return provider.NewError(Name, opVerify, "BAD_SIGNATURE",
			"signature_key mismatch", provider.ErrInvalidSignature)
	}
	return nil
}

// ParseWebhook implements [provider.Provider]. Midtrans posts a JSON
// notification carrying the order_id (our merchant reference), the transaction
// id, and the new transaction_status. It is a pure mapping with no I/O.
func (p *Provider) ParseWebhook(ctx context.Context, req *provider.WebhookRequest) (*provider.WebhookEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opWebhook, err)
	}
	if req == nil || len(req.Body) == 0 {
		return nil, provider.NewError(Name, opWebhook, "EMPTY", "empty callback body", provider.ErrInvalidRequest)
	}
	var n transactionResponse
	if err := json.Unmarshal(req.Body, &n); err != nil {
		return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
			"cannot parse callback body: "+err.Error(), provider.ErrInvalidRequest)
	}

	status := mapTransactionStatus(n.TransactionStatus, n.FraudStatus)
	if status == provider.StatusUnknown {
		return nil, provider.NewError(Name, opWebhook, "UNRECOGNIZED_STATUS",
			strings.TrimSpace(n.TransactionStatus), provider.ErrInvalidRequest)
	}

	amount := provider.Amount(0)
	if n.GrossAmount != "" {
		parsed, err := parseAmount(n.GrossAmount)
		if err != nil {
			return nil, provider.NewError(Name, opWebhook, "BAD_AMOUNT", err.Error(), provider.ErrInvalidRequest)
		}
		amount = parsed
	}

	evt := &provider.WebhookEvent{
		ReferenceID:       strings.TrimSpace(n.OrderID),
		ProviderReference: strings.TrimSpace(n.TransactionID),
		Status:            status,
		Amount:            amount,
		PaidAt:            paidAtIfPaid(status, parseTime(n.SettlementTime)),
		EventType:         eventType(n.PaymentType, n.TransactionStatus),
		Raw:               append(json.RawMessage(nil), req.Body...),
	}
	if !evt.Correlatable() {
		return nil, provider.NewError(Name, opWebhook, "NO_REFERENCE",
			"callback carries neither order_id nor transaction_id", provider.ErrInvalidRequest)
	}
	return evt, nil
}
