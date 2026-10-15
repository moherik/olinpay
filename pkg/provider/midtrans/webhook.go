package midtrans

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/moherik/openpay.id/pkg/provider"
)

// ParseWebhook implements [provider.Provider]. Midtrans' BI-SNAP flow posts one
// of three documented notification shapes to the merchant callback URL:
//
//   - VA payment (POST /v1.0/transfer-va/payment): carries the virtual account
//     number, trxId and, under additionalInfo, the payment flag status and the
//     settlement time.
//   - QRIS payment (POST /v1.0/qr/qr-mpm-notify): carries the original
//     reference numbers and a numeric latestTransactionStatus.
//   - e-wallet payment (POST /v1.0/debit/notify): the same shape as QRIS plus
//     e-wallet-specific fields (originalExternalId, createdTime, finishedTime,
//     additionalInfo.userPaymentDetails).
//
// The body alone is not always enough to tell QRIS from e-wallet — the two
// payloads are structurally very similar — so the family is inferred from the
// distinguishing fields. This only affects the reported event type and the
// acknowledgement code; both are mapped to a unified status identically, and
// Midtrans retries based on the HTTP status, not the ack code.
//
// Signature verification is performed by [Provider.VerifyWebhookSignature],
// which the webhook engine calls before this method.
func (p *Provider) ParseWebhook(ctx context.Context, req *provider.WebhookRequest) (*provider.WebhookEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, provider.WrapError(Name, opWebhook, err)
	}
	if req == nil || len(req.Body) == 0 {
		return nil, provider.NewError(Name, opWebhook, "EMPTY", "empty callback body", provider.ErrInvalidRequest)
	}

	// Peek at the payload to decide which notification shape this is.
	var peek rawNotification
	if err := json.Unmarshal(req.Body, &peek); err != nil {
		return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
			"cannot parse callback body: "+err.Error(), provider.ErrInvalidRequest)
	}

	evt := &provider.WebhookEvent{Raw: append(json.RawMessage(nil), req.Body...)}

	switch notificationKind(peek) {
	case EventVAPayment:
		var payload vaNotification
		if err := json.Unmarshal(req.Body, &payload); err != nil {
			return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
				"cannot parse VA notification: "+err.Error(), provider.ErrInvalidRequest)
		}
		evt.EventType = EventVAPayment
		evt.ReferenceID = strings.TrimSpace(payload.TrxID)
		evt.ProviderReference = firstNonEmpty(payload.VirtualAccountNo, payload.ReferenceNo)
		evt.Status = vaNotificationStatus(payload)
		evt.Amount = amountOr(payload.PaidAmount, amountOr(payload.TotalAmount, 0))
		evt.PaidAt = paidAtIfPaid(evt.Status, parseTime(payload.AdditionalInfo.PaidTime))
	case EventQRISPayment:
		var payload qrisNotification
		if err := json.Unmarshal(req.Body, &payload); err != nil {
			return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
				"cannot parse QRIS notification: "+err.Error(), provider.ErrInvalidRequest)
		}
		evt.EventType = EventQRISPayment
		evt.ReferenceID = strings.TrimSpace(payload.OriginalPartnerReferenceNo)
		evt.ProviderReference = strings.TrimSpace(payload.OriginalReferenceNo)
		evt.Status = mapTransactionStatus(payload.LatestTransactionStatus)
		evt.Amount = amountOr(payload.Amount, 0)
	case EventEWalletPayment:
		var payload ewalletNotification
		if err := json.Unmarshal(req.Body, &payload); err != nil {
			return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
				"cannot parse e-wallet notification: "+err.Error(), provider.ErrInvalidRequest)
		}
		evt.EventType = EventEWalletPayment
		evt.ReferenceID = firstNonEmpty(payload.OriginalPartnerReferenceNo, payload.OriginalExternalID)
		evt.ProviderReference = strings.TrimSpace(payload.OriginalReferenceNo)
		evt.Status = mapTransactionStatus(payload.LatestTransactionStatus)
		evt.Amount = amountOr(payload.Amount, 0)
		evt.PaidAt = paidAtIfPaid(evt.Status, parseTime(payload.FinishedTime))
	default:
		return nil, provider.NewError(Name, opWebhook, "UNKNOWN_SHAPE",
			"callback body matches no known Midtrans BI-SNAP notification shape", provider.ErrInvalidRequest)
	}

	if !evt.Correlatable() {
		return nil, provider.NewError(Name, opWebhook, "NOT_CORRELATABLE",
			"notification carries neither a merchant reference nor a provider reference", provider.ErrInvalidRequest)
	}
	return evt, nil
}

// notificationKind classifies an inbound notification by its discriminating
// fields. It returns one of the Event* constants.
func notificationKind(peek rawNotification) string {
	switch {
	case peek.VirtualAccountNo != "" || peek.PartnerServiceID != "":
		return EventVAPayment
	case present(peek.AdditionalInfo.UserPaymentDetails),
		peek.OriginalExternalID != "",
		peek.CreatedTime != "",
		peek.FinishedTime != "":
		return EventEWalletPayment
	case present(peek.AdditionalInfo.QRIS), peek.ExternalStoreID != "":
		return EventQRISPayment
	case peek.LatestTransactionStatus != "":
		// Both remaining families share latestTransactionStatus; without any
		// discriminating field prefer e-wallet. The ack code differs but
		// Midtrans retries on the HTTP status, so a wrong guess is harmless.
		return EventEWalletPayment
	default:
		return ""
	}
}

// present reports whether a raw JSON field was supplied and is not null.
func present(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// vaNotificationStatus derives the unified status of a VA payment
// notification. The paymentFlagStatus follows the SNAP numeric codes; the
// notification is only sent on completion, so an absent or unrecognized flag
// falls back to paid.
func vaNotificationStatus(payload vaNotification) provider.Status {
	if status := mapTransactionStatus(payload.AdditionalInfo.PaymentFlagStatus); status != provider.StatusUnknown {
		return status
	}
	return provider.StatusPaid
}

// VerifyWebhookSignature implements [provider.SignatureVerifier]. Midtrans signs
// outbound notifications with its own RSA private key (SHA256withRSA, PKCS#1
// v1.5) over
//
//	HTTPMethod:EndpointUrl:Lowercase(HexEncode(SHA-256(minify(RequestBody)))):X-TIMESTAMP
//
// and the merchant verifies with the public key Midtrans shares during
// onboarding. The EndpointUrl is the notification path; because Midtrans
// documents it only as "EndpointUrl" and sends no endpoint header, each
// candidate path (the received path, then the three documented notification
// paths) is tried in turn. Only a signature produced by Midtrans' private key
// can match, so trying several candidates does not weaken authentication.
//
// When public_key is not configured the callback is rejected rather than
// trusted.
func (p *Provider) VerifyWebhookSignature(req *provider.WebhookRequest) error {
	if req == nil {
		return provider.NewError(Name, opVerify, "EMPTY", "empty callback", provider.ErrInvalidSignature)
	}
	if p.publicKey == nil {
		return provider.NewError(Name, opVerify, "NO_PUBLIC_KEY",
			"public_key is not configured; refusing to accept unauthenticated callbacks",
			provider.ErrInvalidSignature)
	}
	if len(req.Body) == 0 {
		return provider.NewError(Name, opVerify, "EMPTY", "empty callback body", provider.ErrInvalidSignature)
	}
	signature := req.Header(headerSignature)
	timestamp := req.Header(headerTimestamp)
	if signature == "" || timestamp == "" {
		return provider.NewError(Name, opVerify, "MISSING_HEADERS",
			"callback is missing "+headerSignature+" or "+headerTimestamp, provider.ErrInvalidSignature)
	}

	for _, endpoint := range signatureEndpoints(req.Path) {
		if err := verifyNotificationSignature(p.publicKey, http.MethodPost, endpoint, req.Body, timestamp, signature); err == nil {
			return nil
		}
	}
	return provider.NewError(Name, opVerify, "BAD_SIGNATURE",
		"X-SIGNATURE does not match the callback body", provider.ErrInvalidSignature)
}

// signatureEndpoints returns the candidate EndpointUrl values, most likely
// first, de-duplicated.
func signatureEndpoints(path string) []string {
	candidates := []string{strings.TrimSpace(path), pathVANotify, pathQRISNotify, pathDBNotify}
	seen := make(map[string]struct{}, len(candidates))
	out := candidates[:0]
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// WebhookResponse implements [provider.WebhookResponder], returning the SNAP
// acknowledgement Midtrans expects for the notification family. Midtrans
// retries on any non-200 HTTP status, but returning its documented response
// code keeps the exchange well-formed.
func (p *Provider) WebhookResponse(evt *provider.WebhookEvent) (int, []byte) {
	code := responseEWalletPayment
	if evt != nil {
		switch evt.EventType {
		case EventVAPayment:
			code = responseVAPayment
		case EventQRISPayment:
			code = responseQRISPayment
		}
	}
	body, err := json.Marshal(apiResponse{ResponseCode: code, ResponseMessage: "Successful"})
	if err != nil {
		return http.StatusOK, []byte(`{"responseCode":"` + code + `","responseMessage":"Successful"}`)
	}
	return http.StatusOK, body
}
