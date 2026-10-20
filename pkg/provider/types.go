package provider

import (
	"encoding/json"
	"time"
)

// Currency is an ISO-4217 currency code (e.g. "IDR").
type Currency string

// IDR is the Indonesian Rupiah, the primary currency handled by openpay.id.
const IDR Currency = "IDR"

// Amount is a monetary value expressed in the smallest unit of the currency.
// The provider contract carries money as a plain int64 in [CreatePaymentRequest].
// Note that for IDR the smallest practical unit is the rupiah itself.
type Amount = int64

// Customer is the buyer/payer identity passed through to the gateway.
type Customer struct {
	// Name is the customer's display name.
	Name string `json:"name"`
	// Email is optional but recommended for e-wallet and card flows.
	Email string `json:"email,omitempty"`
	// Phone is the MSISDN in E.164 or local format, required by some gateways.
	Phone string `json:"phone,omitempty"`
	// Reference is an optional merchant-side identifier for the customer.
	Reference string `json:"reference,omitempty"`
}

// Item is a single line item of the payment.
type Item struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Price    Amount `json:"price"`
	Quantity int    `json:"quantity"`
}

// Total returns the aggregate price of the item.
func (i Item) Total() Amount { return i.Price * Amount(i.Quantity) }

// ActionType describes how a [PaymentAction] should be presented to the payer.
type ActionType string

const (
	// ActionURL is a URL the payer must open (e.g. an e-wallet deeplink or a
	// hosted payment page).
	ActionURL ActionType = "url"
	// ActionDeeplink is an app deeplink (e.g. "gopay://...").
	ActionDeeplink ActionType = "deeplink"
	// ActionQRCode is a QR payload the payer scans.
	ActionQRCode ActionType = "qrcode"
	// ActionDisplay is a value that must be shown to the payer as text, such
	// as a virtual account number or a retail payment code.
	ActionDisplay ActionType = "display"
)

// PaymentAction is an instruction returned by a gateway telling the caller how
// to complete the payment. A single create-payment call may return several
// actions (e.g. a deeplink plus a QR code).
type PaymentAction struct {
	Type  ActionType `json:"type"`
	Value string     `json:"value"`
	// Description is an optional human readable hint for the action.
	Description string `json:"description,omitempty"`
}

// CreatePaymentRequest is the unified input for [Provider.CreatePayment].
//
// All fields are gateway-agnostic. The provider is responsible for mapping
// Channel and Method onto the gateway's own product code.
type CreatePaymentRequest struct {
	// ReferenceID is the merchant-facing unique identifier of this payment.
	// It MUST be treated as the idempotency key towards the gateway.
	ReferenceID string `json:"reference_id"`
	// Amount is the total to charge, in the smallest currency unit.
	Amount Amount `json:"amount"`
	// Currency is the ISO-4217 code; defaults to IDR when empty.
	Currency Currency `json:"currency"`
	// Method is the unified payment method category.
	Method Method `json:"method"`
	// Channel selects a concrete instrument within the method, using the
	// gateway's own vocabulary (e.g. "BCA", "GOPAY", "QRIS"). The core engine
	// passes it through untouched.
	Channel string `json:"channel,omitempty"`
	// Customer is the payer identity.
	Customer Customer `json:"customer"`
	// Items are the line items backing Amount. Optional.
	Items []Item `json:"items,omitempty"`
	// Description is a free-form label shown to the payer.
	Description string `json:"description,omitempty"`
	// Expiry is the requested lifetime of the payment. Zero means the gateway
	// default is used.
	Expiry time.Duration `json:"-"`
	// CallbackURL, when set, overrides the gateway's globally configured
	// webhook URL for this single payment.
	CallbackURL string `json:"callback_url,omitempty"`
	// ReturnURL is the merchant's payer redirect target after the payment flow.
	ReturnURL string `json:"-"`
	// SuccessReturnURL and FailureReturnURL override ReturnURL where supported.
	SuccessReturnURL string `json:"-"`
	FailureReturnURL string `json:"-"`
	// Metadata is opaque key/value data echoed back on responses and webhooks.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// CreatePaymentResponse is the unified output for [Provider.CreatePayment].
type CreatePaymentResponse struct {
	// ProviderReference is the gateway's own transaction identifier. It is the
	// key used later for Inquiry/Cancel and for correlating webhooks.
	ProviderReference string            `json:"provider_reference"`
	ProviderData      map[string]string `json:"-"`
	// ReferenceID echoes back the merchant reference from the request so the
	// core engine can correlate even if the gateway mangles it.
	ReferenceID string `json:"reference_id"`
	// Status is the normalized status right after creation.
	Status Status `json:"status"`
	// Amount is the amount actually registered by the gateway. Providers must
	// return the gateway value here; the core engine verifies it matches.
	Amount Amount `json:"amount"`
	// PaymentCode is the VA number, retail code, or similar textual payment
	// identifier. Empty when not applicable (e.g. pure deeplink flows).
	PaymentCode string `json:"payment_code,omitempty"`
	// Actions describe how the payer completes the payment.
	Actions []PaymentAction `json:"actions,omitempty"`
	// ExpiresAt is the absolute expiry instant, if the gateway returns one.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Raw is the untouched gateway payload, retained for auditing/debugging.
	Raw json.RawMessage `json:"raw,omitempty"`
}

// InquiryRequest is the unified input for [Provider.Inquiry].
type InquiryRequest struct {
	// ReferenceID is the merchant reference used at creation time.
	ReferenceID string `json:"reference_id"`
	// ProviderReference is the gateway identifier, if known.
	ProviderReference string            `json:"provider_reference,omitempty"`
	ProviderData      map[string]string `json:"-"`
}

// InquiryResponse is the unified output for [Provider.Inquiry].
type InquiryResponse struct {
	ReferenceID       string          `json:"reference_id"`
	ProviderReference string          `json:"provider_reference"`
	Status            Status          `json:"status"`
	Amount            Amount          `json:"amount"`
	PaymentCode       string          `json:"payment_code,omitempty"`
	PaidAt            *time.Time      `json:"paid_at,omitempty"`
	ExpiresAt         *time.Time      `json:"expires_at,omitempty"`
	Raw               json.RawMessage `json:"raw,omitempty"`
}

// CancelRequest is the unified input for [Provider.Cancel].
//
// A single operation covers both explicit cancellation and forced expiry, since
// most gateways expose a single "void/cancel/expire" endpoint. The Reason field
// lets the core engine communicate intent.
type CancelRequest struct {
	ReferenceID       string            `json:"reference_id"`
	ProviderReference string            `json:"provider_reference,omitempty"`
	ProviderData      map[string]string `json:"-"`
	// Reason is either "cancel" or "expire" by convention, or any gateway
	// specific note. Providers may forward it when the gateway supports it.
	Reason string `json:"reason,omitempty"`
}

// CancelResponse is the unified output for [Provider.Cancel].
type CancelResponse struct {
	ReferenceID       string          `json:"reference_id"`
	ProviderReference string          `json:"provider_reference"`
	Status            Status          `json:"status"`
	Raw               json.RawMessage `json:"raw,omitempty"`
}
