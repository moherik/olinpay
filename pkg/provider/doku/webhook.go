package doku

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/moherik/openpay.id/pkg/provider"
)

// ParseWebhook implements [provider.Provider]. It recognizes the two documented
// DOKU SNAP callback shapes and maps them onto a unified [provider.WebhookEvent]:
//
//   - Virtual Account payment notification (POST /v1/transfer-va/payment):
//     always a settled payment, correlated by trxId and the VA number.
//   - Direct-debit / e-wallet notification (POST /v1.0/debit/notify): carries a
//     numeric latestTransactionStatus, correlated by the original partner
//     reference number.
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

	switch {
	case peek.VirtualAccountNo != "" || peek.PartnerServiceID != "":
		var payload vaNotification
		if err := json.Unmarshal(req.Body, &payload); err != nil {
			return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
				"cannot parse VA notification: "+err.Error(), provider.ErrInvalidRequest)
		}
		evt.EventType = EventVAPayment
		evt.ReferenceID = strings.TrimSpace(payload.TrxID)
		evt.ProviderReference = strings.TrimSpace(payload.VirtualAccountNo)
		evt.Status = provider.StatusPaid
		evt.Amount = amountOr(payload.PaidAmount, 0)
		if t := parseTime(payload.TransactionDate); t != nil {
			evt.PaidAt = t
		}
	case peek.LatestTransactionStatus != "":
		var payload ddNotification
		if err := json.Unmarshal(req.Body, &payload); err != nil {
			return nil, provider.NewError(Name, opWebhook, "BAD_JSON",
				"cannot parse direct-debit notification: "+err.Error(), provider.ErrInvalidRequest)
		}
		evt.EventType = EventEWalletPayment
		evt.ReferenceID = strings.TrimSpace(payload.OriginalPartnerReferenceNo)
		evt.ProviderReference = strings.TrimSpace(payload.OriginalReferenceNo)
		evt.Status = mapTransactionStatus(payload.LatestTransactionStatus)
		evt.Amount = amountOr(payload.Amount, 0)
	default:
		return nil, provider.NewError(Name, opWebhook, "UNKNOWN_SHAPE",
			"callback body matches no known DOKU notification shape", provider.ErrInvalidRequest)
	}

	if !evt.Correlatable() {
		return nil, provider.NewError(Name, opWebhook, "NOT_CORRELATABLE",
			"notification carries neither a merchant reference nor a provider reference", provider.ErrInvalidRequest)
	}
	return evt, nil
}

// VerifyWebhookSignature implements [provider.SignatureVerifier]. DOKU signs
// outbound notifications with HMAC-SHA512 using the merchant's client_secret
// over
//
//	HTTP-METHOD:RELATIVE-URL:ACCESS-TOKEN:HEX(SHA256(requestBody)):TIMESTAMP
//
// The access token DOKU folds into the signature is not documented precisely
// (it may be the B2B token, the B2B2C token, or empty), so each candidate is
// tried in turn. The comparison is constant-time. When client_secret is not
// configured the callback is rejected rather than trusted.
func (p *Provider) VerifyWebhookSignature(req *provider.WebhookRequest) error {
	if req == nil {
		return provider.NewError(Name, opVerify, "EMPTY", "empty callback", provider.ErrInvalidSignature)
	}
	if p.clientSecret == "" {
		return provider.NewError(Name, opVerify, "NO_SECRET",
			"client_secret is not configured; refusing to accept unauthenticated callbacks",
			provider.ErrInvalidSignature)
	}
	signature := req.Header(headerSignature)
	timestamp := req.Header(headerTimestamp)
	if signature == "" || timestamp == "" {
		return provider.NewError(Name, opVerify, "MISSING_HEADERS",
			"callback is missing "+headerSignature+" or "+headerTimestamp, provider.ErrInvalidSignature)
	}
	endpoint := firstNonEmpty(req.Path, pathVANotify)
	accessToken := strings.TrimPrefix(req.Header("Authorization"), "Bearer ")

	if err := verifyNotificationSignature(p.clientSecret, http.MethodPost, endpoint, accessToken, req.Body, timestamp, signature); err != nil {
		return provider.NewError(Name, opVerify, "BAD_SIGNATURE", err.Error(), provider.ErrInvalidSignature)
	}
	return nil
}

// WebhookResponse implements [provider.WebhookResponder], returning the SNAP
// acknowledgement DOKU expects (otherwise it retries the callback).
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
