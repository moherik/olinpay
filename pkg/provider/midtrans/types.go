package midtrans

import "strings"

// This file mirrors the payload shapes published for Midtrans' legacy Core API
// (https://docs.midtrans.com/reference/core-api-overview). Field names and JSON
// tags are kept as-is so the mapping stays obvious; unified translation happens
// in midtrans.go, map.go and webhook.go.

// apiResponse is embedded into every Core API status envelope. Midtrans reports
// its outcome as a *string* status_code ("201" created, "200" ok, "406"
// duplicate order id, ...) even when the HTTP status is 200, so callers must
// inspect it rather than the transport status alone.
type apiResponse struct {
	StatusCode         string              `json:"status_code"`
	StatusMessage      string              `json:"status_message"`
	ValidationMessages []validationMessage `json:"validation_messages,omitempty"`
}

// success reports whether the response carries a 2xx status_code.
func (r apiResponse) success() bool {
	return strings.HasPrefix(strings.TrimSpace(r.StatusCode), "2")
}

// validationMessage describes a single rejected request field.
type validationMessage struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// chargeRequest is the body of POST /v2/charge. Only the payment-type-specific
// block matching PaymentType is populated.
type chargeRequest struct {
	PaymentType        string             `json:"payment_type"`
	TransactionDetails transactionDetails `json:"transaction_details"`
	CustomerDetails    *customerDetails   `json:"customer_details,omitempty"`
	ItemDetails        []itemDetail       `json:"item_details,omitempty"`
	CustomExpiry       *customExpiry      `json:"custom_expiry,omitempty"`
	BankTransfer       *bankTransfer      `json:"bank_transfer,omitempty"`
	ECHannel           *echannel          `json:"echannel,omitempty"`
	QRIS               *qris              `json:"qris,omitempty"`
	GoPay              *gopay             `json:"gopay,omitempty"`
	ShopeePay          *shopeePay         `json:"shopeepay,omitempty"`
	CStore             *cstore            `json:"cstore,omitempty"`
}

// transactionDetails carries the merchant order id and gross amount.
type transactionDetails struct {
	OrderID     string `json:"order_id"`
	GrossAmount int64  `json:"gross_amount"`
}

// customerDetails is the payer block; Midtrans splits a display name into a
// first and last name.
type customerDetails struct {
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// itemDetail is one line item.
type itemDetail struct {
	ID       string `json:"id,omitempty"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
	Name     string `json:"name"`
}

// customExpiry sets the payment lifetime for VA/QRIS. Midtrans expects an
// absolute order_time with a numeric offset, a duration and a unit.
type customExpiry struct {
	OrderTime      string `json:"order_time"`
	ExpiryDuration int    `json:"expiry_duration"`
	Unit           string `json:"unit"`
}

// bankTransfer selects a virtual account bank on a bank_transfer charge.
type bankTransfer struct {
	Bank     string `json:"bank"`
	VANumber string `json:"va_number,omitempty"`
}

// echannel selects Mandiri Bill Payment (biller code + bill key).
type echannel struct {
	BillInfo1 string `json:"bill_info1,omitempty"`
	BillInfo2 string `json:"bill_info2,omitempty"`
}

// qris selects the QRIS acquirer.
type qris struct {
	Acquirer string `json:"acquirer,omitempty"`
}

// gopay enables the GoPay flow and its return callback.
type gopay struct {
	EnableCallback bool   `json:"enable_callback"`
	CallbackURL    string `json:"callback_url,omitempty"`
}

// shopeePay enables the ShopeePay flow and its return callback.
type shopeePay struct {
	CallbackURL string `json:"callback_url,omitempty"`
}

// cstore selects a retail outlet.
type cstore struct {
	Store   string `json:"store"`
	Message string `json:"message,omitempty"`
}

// chargeResponse is the shared shape returned by /v2/charge, /v2/{id}/status,
// /v2/{id}/cancel and /v2/{id}/expire. Which payment-type fields are present
// depends on the transaction.
type chargeResponse struct {
	apiResponse
	TransactionID     string     `json:"transaction_id"`
	OrderID           string     `json:"order_id"`
	GrossAmount       string     `json:"gross_amount"`
	Currency          string     `json:"currency"`
	PaymentType       string     `json:"payment_type"`
	TransactionTime   string     `json:"transaction_time"`
	TransactionStatus string     `json:"transaction_status"`
	FraudStatus       string     `json:"fraud_status"`
	SettlementTime    string     `json:"settlement_time"`
	ExpiryTime        string     `json:"expiry_time"`
	VANumbers         []vaNumber `json:"va_numbers"`
	PermataVANumber   string     `json:"permata_va_number"`
	BillerCode        string     `json:"biller_code"`
	BillKey           string     `json:"bill_key"`
	PaymentCode       string     `json:"payment_code"`
	Store             string     `json:"store"`
	Acquirer          string     `json:"acquirer"`
	QRString          string     `json:"qr_string"`
	MaskedCard        string     `json:"masked_card"`
	Actions           []action   `json:"actions"`
}

// vaNumber is one virtual account number issued by a bank_transfer charge.
type vaNumber struct {
	Bank     string `json:"bank"`
	VANumber string `json:"va_number"`
}

// action is an instruction Midtrans returns for QRIS and e-wallet charges.
type action struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	URL    string `json:"url"`
}

// notification is the JSON body Midtrans POSTs to the merchant notification
// URL. It reuses the transaction fields of chargeResponse and adds the
// signature_key that authenticates the callback.
type notification struct {
	TransactionTime   string     `json:"transaction_time"`
	TransactionStatus string     `json:"transaction_status"`
	TransactionID     string     `json:"transaction_id"`
	StatusMessage     string     `json:"status_message"`
	StatusCode        string     `json:"status_code"`
	SignatureKey      string     `json:"signature_key"`
	PaymentType       string     `json:"payment_type"`
	OrderID           string     `json:"order_id"`
	GrossAmount       string     `json:"gross_amount"`
	Currency          string     `json:"currency"`
	FraudStatus       string     `json:"fraud_status"`
	SettlementTime    string     `json:"settlement_time"`
	ExpiryTime        string     `json:"expiry_time"`
	VANumbers         []vaNumber `json:"va_numbers"`
	PermataVANumber   string     `json:"permata_va_number"`
	BillerCode        string     `json:"biller_code"`
	BillKey           string     `json:"bill_key"`
	PaymentCode       string     `json:"payment_code"`
	Store             string     `json:"store"`
	Acquirer          string     `json:"acquirer"`
	MaskedCard        string     `json:"masked_card"`
}
