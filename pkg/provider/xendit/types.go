package xendit

import "encoding/json"

// This file mirrors the payload shapes of Xendit's Payments API v3
// (https://docs.xendit.co/apidocs/create-payment-request). Field names and JSON
// tags are kept as-is so the mapping stays obvious; unified translation happens
// in xendit.go.

// Payment request types ("type").
const (
	typePay                 = "PAY"
	typeReusablePaymentCode = "REUSABLE_PAYMENT_CODE"
)

// Channel codes that are not selected by a caller-supplied bank/wallet code.
const (
	channelQRIS  = "QRIS"
	channelCards = "CARDS"
)

// Action types and descriptors returned in the "actions" array.
const (
	actionRedirect = "REDIRECT_CUSTOMER"
	actionPresent  = "PRESENT_TO_CUSTOMER"

	descriptorVANumber    = "VIRTUAL_ACCOUNT_NUMBER"
	descriptorPaymentCode = "PAYMENT_CODE"
	descriptorQRString    = "QR_STRING"
	descriptorWebURL      = "WEB_URL"
	descriptorDeeplink    = "DEEPLINK_URL"
)

// --- requests --------------------------------------------------------------

// paymentRequest is the body of POST /v3/payment_requests. Method-specific data
// is carried by Type, ChannelCode and the free-form ChannelProperties object.
type paymentRequest struct {
	ReferenceID       string            `json:"reference_id"`
	Type              string            `json:"type"`
	Country           string            `json:"country"`
	Currency          string            `json:"currency"`
	RequestAmount     int64             `json:"request_amount"`
	CaptureMethod     string            `json:"capture_method,omitempty"`
	ChannelCode       string            `json:"channel_code,omitempty"`
	ChannelProperties map[string]any    `json:"channel_properties,omitempty"`
	PaymentTokenID    string            `json:"payment_token_id,omitempty"`
	Description       string            `json:"description,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// --- responses and notifications -------------------------------------------

// action is one entry of the "actions" array Xendit returns for flows that need
// a QR payload, a payment code or a redirect.
type action struct {
	Type       string `json:"type"`
	Value      string `json:"value"`
	Descriptor string `json:"descriptor"`
}

type capture struct {
	CaptureID        string      `json:"capture_id"`
	CaptureTimestamp string      `json:"capture_timestamp"`
	CaptureAmount    json.Number `json:"capture_amount"`
}

// payment is the "payment" object attached to a payment request (latest_payment)
// and to payment.* webhooks.
type payment struct {
	PaymentID        string      `json:"payment_id"`
	BusinessID       string      `json:"business_id"`
	ReferenceID      string      `json:"reference_id"`
	PaymentRequestID string      `json:"payment_request_id"`
	PaymentTokenID   string      `json:"payment_token_id"`
	ChannelCode      string      `json:"channel_code"`
	Status           string      `json:"status"`
	RequestAmount    json.Number `json:"request_amount"`
	Captures         []capture   `json:"captures"`
	FailureCode      string      `json:"failure_code"`
	Created          string      `json:"created"`
	Updated          string      `json:"updated"`
}

// paymentRequestResponse is the superset of the create, get, cancel and
// expiry-notification payloads. Xendit returns the same fields for all of them,
// so a single struct covers them.
type paymentRequestResponse struct {
	BusinessID        string            `json:"business_id"`
	ReferenceID       string            `json:"reference_id"`
	PaymentRequestID  string            `json:"payment_request_id"`
	PaymentTokenID    string            `json:"payment_token_id"`
	CustomerID        string            `json:"customer_id"`
	LatestPaymentID   string            `json:"latest_payment_id"`
	LatestPayment     *payment          `json:"latest_payment"`
	Type              string            `json:"type"`
	Country           string            `json:"country"`
	Currency          string            `json:"currency"`
	RequestAmount     json.Number       `json:"request_amount"`
	CaptureMethod     string            `json:"capture_method"`
	ChannelCode       string            `json:"channel_code"`
	ChannelProperties map[string]any    `json:"channel_properties"`
	Actions           []action          `json:"actions"`
	Status            string            `json:"status"`
	FailureCode       string            `json:"failure_code"`
	Description       string            `json:"description"`
	Metadata          map[string]string `json:"metadata"`
	Created           string            `json:"created"`
	Updated           string            `json:"updated"`
}

// errorResponse is Xendit's error envelope.
type errorResponse struct {
	ErrorCode string `json:"error_code"`
	Message   string `json:"message"`
}

// --- webhooks --------------------------------------------------------------

// webhookEnvelope is the outer shape of every Payments API callback:
// {"event": "payment.capture", "business_id": "...", "created": "...", "data": {...}}.
type webhookEnvelope struct {
	Event      string          `json:"event"`
	BusinessID string          `json:"business_id"`
	Created    string          `json:"created"`
	Data       json.RawMessage `json:"data"`
}

// webhookData is the union of the payment.* and payment_request.* data objects.
type webhookData struct {
	PaymentID        string      `json:"payment_id"`
	ReferenceID      string      `json:"reference_id"`
	PaymentRequestID string      `json:"payment_request_id"`
	PaymentTokenID   string      `json:"payment_token_id"`
	Type             string      `json:"type"`
	ChannelCode      string      `json:"channel_code"`
	Status           string      `json:"status"`
	RequestAmount    json.Number `json:"request_amount"`
	Captures         []capture   `json:"captures"`
	FailureCode      string      `json:"failure_code"`
	Created          string      `json:"created"`
	Updated          string      `json:"updated"`
}
