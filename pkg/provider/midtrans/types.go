package midtrans

// This file mirrors the payload shapes of Midtrans' Core API
// (https://docs.midtrans.com/reference/charge-transactions-1). Field names and
// JSON tags are kept as-is so the mapping stays obvious; unified translation
// happens in midtrans.go.

// Payment types ("payment_type") used by the Core API.
const (
	paymentTypeBankTransfer = "bank_transfer"
	paymentTypeEChannel     = "echannel"
	paymentTypeGoPay        = "gopay"
	paymentTypeShopeePay    = "shopeepay"
	paymentTypeQRIS         = "qris"
	paymentTypeCStore       = "cstore"
	paymentTypeCreditCard   = "credit_card"
	paymentTypeAkulaku      = "akulaku"
)

// --- requests --------------------------------------------------------------

type transactionDetails struct {
	OrderID     string `json:"order_id"`
	GrossAmount int64  `json:"gross_amount"`
}

type customerDetails struct {
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

type itemDetail struct {
	ID       string `json:"id,omitempty"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
	Name     string `json:"name"`
}

// customExpiry bounds the payment lifetime. Midtrans supports it for bank
// transfers, e-channel, over-the-counter and direct debit, but not for QRIS,
// e-wallets or cards.
type customExpiry struct {
	ExpiryDuration int    `json:"expiry_duration"`
	Unit           string `json:"unit"`
}

type bankTransferDetail struct {
	Bank string `json:"bank"`
}

type echannelDetail struct {
	BillInfo1 string `json:"bill_info1,omitempty"`
	BillInfo2 string `json:"bill_info2,omitempty"`
}

type gopayDetail struct {
	EnableCallback bool   `json:"enable_callback"`
	CallbackURL    string `json:"callback_url,omitempty"`
}

type shopeepayDetail struct {
	CallbackURL string `json:"callback_url,omitempty"`
}

type qrisDetail struct {
	Acquirer string `json:"acquirer,omitempty"`
}

type cstoreDetail struct {
	Store   string `json:"store"`
	Message string `json:"message,omitempty"`
}

type creditCardDetail struct {
	TokenID        string `json:"token_id"`
	Authentication bool   `json:"authentication"`
}

// akulakuDetail has no required fields; the zero struct is marshalled as {}.
type akulakuDetail struct{}

// chargeRequest is the body of POST /v2/charge. Method-specific objects are
// populated according to payment_type and omitted otherwise.
type chargeRequest struct {
	PaymentType        string             `json:"payment_type"`
	TransactionDetails transactionDetails `json:"transaction_details"`
	CustomerDetails    *customerDetails   `json:"customer_details,omitempty"`
	ItemDetails        []itemDetail       `json:"item_details,omitempty"`
	CustomExpiry       *customExpiry      `json:"custom_expiry,omitempty"`
	Metadata           map[string]string  `json:"metadata,omitempty"`

	BankTransfer *bankTransferDetail `json:"bank_transfer,omitempty"`
	EChannel     *echannelDetail     `json:"echannel,omitempty"`
	GoPay        *gopayDetail        `json:"gopay,omitempty"`
	ShopeePay    *shopeepayDetail    `json:"shopeepay,omitempty"`
	QRIS         *qrisDetail         `json:"qris,omitempty"`
	CStore       *cstoreDetail       `json:"cstore,omitempty"`
	CreditCard   *creditCardDetail   `json:"credit_card,omitempty"`
	Akulaku      *akulakuDetail      `json:"akulaku,omitempty"`
}

// --- responses and notifications -------------------------------------------

// action is one entry of the "actions" array Midtrans returns for flows that
// need a QR image, deeplink or redirect page.
type action struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	URL    string `json:"url"`
}

type vaNumber struct {
	Bank     string `json:"bank"`
	VANumber string `json:"va_number"`
}

// transactionResponse is the superset of the charge, status and notification
// payloads. Midtrans returns the same field names in all three, so a single
// struct covers them.
type transactionResponse struct {
	StatusCode        string `json:"status_code"`
	StatusMessage     string `json:"status_message"`
	TransactionID     string `json:"transaction_id"`
	OrderID           string `json:"order_id"`
	MerchantID        string `json:"merchant_id"`
	GrossAmount       string `json:"gross_amount"`
	Currency          string `json:"currency"`
	PaymentType       string `json:"payment_type"`
	TransactionTime   string `json:"transaction_time"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	ExpiryTime        string `json:"expiry_time"`
	SettlementTime    string `json:"settlement_time"`
	SignatureKey      string `json:"signature_key"`

	// Virtual account / e-channel / over-the-counter identifiers.
	VANumbers       []vaNumber `json:"va_numbers"`
	PermataVANumber string     `json:"permata_va_number"`
	BillKey         string     `json:"bill_key"`
	BillerCode      string     `json:"biller_code"`
	PaymentCode     string     `json:"payment_code"`
	Store           string     `json:"store"`

	// Card / redirect flows.
	RedirectURL  string   `json:"redirect_url"`
	MaskedCard   string   `json:"masked_card"`
	Actions      []action `json:"actions"`
	Acquirer     string   `json:"acquirer"`
	Channel      string   `json:"channel"`
	RefundAmount string   `json:"refund_amount"`
}
