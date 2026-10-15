package midtranssnap

import (
	"encoding/json"
	"strconv"
	"strings"
)

// This file mirrors the payload shapes published in Midtrans' BI-SNAP Core API
// documentation (https://docs.midtrans.com/reference/core-api-snap-open-api-overview).
// Field names and JSON tags are kept as-is so the mapping stays obvious; unified
// translation happens in midtrans.go, map.go and webhook.go.

// flexInt unmarshals a JSON number or a numeric string into an int. Midtrans is
// inconsistent across endpoints — most amounts and codes are strings, but the
// access-token expiry is documented as a string ("900") while some tooling emits
// a bare number — so this tolerates both.
type flexInt int

// UnmarshalJSON implements [json.Unmarshaler].
func (f *flexInt) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*f = 0
		return nil
	}
	raw = strings.Trim(raw, `"`)
	if raw == "" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return err
	}
	*f = flexInt(n)
	return nil
}

// amountPayload is the {"value","currency"} object used by the BI-SNAP
// endpoints, both on requests and on responses. IDR values carry two decimals,
// e.g. 10000.00.
type amountPayload struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

// apiResponse is embedded into every response envelope.
type apiResponse struct {
	ResponseCode    string `json:"responseCode"`
	ResponseMessage string `json:"responseMessage"`
}

// --- Access Token ----------------------------------------------------------

type tokenRequest struct {
	GrantType string `json:"grantType"`
}

type tokenResponse struct {
	apiResponse
	AccessToken string  `json:"accessToken"`
	TokenType   string  `json:"tokenType"`
	ExpiresIn   flexInt `json:"expiresIn"`
}

// --- Virtual Account -------------------------------------------------------

// createVARequest is the body of POST /v1.0/transfer-va/create-va.
type createVARequest struct {
	PartnerServiceID    string         `json:"partnerServiceId"`
	CustomerNo          string         `json:"customerNo"`
	VirtualAccountNo    string         `json:"virtualAccountNo"`
	VirtualAccountName  string         `json:"virtualAccountName"`
	VirtualAccountEmail string         `json:"virtualAccountEmail,omitempty"`
	VirtualAccountPhone string         `json:"virtualAccountPhone,omitempty"`
	TrxID               string         `json:"trxId"`
	TotalAmount         *amountPayload `json:"totalAmount,omitempty"`
	ExpiredDate         string         `json:"expiredDate,omitempty"`
	AdditionalInfo      vaAdditional   `json:"additionalInfo"`
}

// vaAdditional is the additionalInfo object of the create-va request.
type vaAdditional struct {
	MerchantID string         `json:"merchantId,omitempty"`
	Bank       string         `json:"bank,omitempty"`
	Mandiri    *mandiriDetail `json:"mandiri,omitempty"`
}

// mandiriDetail carries the free-text labels Mandiri shows to the payer.
type mandiriDetail struct {
	BillInfo1 string `json:"billInfo1,omitempty"`
	BillInfo2 string `json:"billInfo2,omitempty"`
}

// createVAResponse is the envelope returned by create-va.
type createVAResponse struct {
	apiResponse
	VirtualAccountData vaData `json:"virtualAccountData"`
}

// vaData carries the virtual account details returned by create-va.
type vaData struct {
	PartnerServiceID    string         `json:"partnerServiceId"`
	CustomerNo          string         `json:"customerNo"`
	VirtualAccountNo    string         `json:"virtualAccountNo"`
	VirtualAccountName  string         `json:"virtualAccountName"`
	VirtualAccountEmail string         `json:"virtualAccountEmail"`
	VirtualAccountPhone string         `json:"virtualAccountPhone"`
	TrxID               string         `json:"trxId"`
	TotalAmount         *amountPayload `json:"totalAmount"`
	ExpiredDate         string         `json:"expiredDate"`
	AdditionalInfo      struct {
		MerchantID string `json:"merchantId"`
		Bank       string `json:"bank"`
	} `json:"additionalInfo"`
}

// vaStatusRequest is the body of POST /v1.0/transfer-va/status.
type vaStatusRequest struct {
	PartnerServiceID string `json:"partnerServiceId"`
	CustomerNo       string `json:"customerNo"`
	VirtualAccountNo string `json:"virtualAccountNo"`
	InquiryRequestID string `json:"inquiryRequestId,omitempty"`
	PaymentRequestID string `json:"paymentRequestId,omitempty"`
	AdditionalInfo   struct {
		MerchantID string `json:"merchant_id,omitempty"`
	} `json:"additionalInfo"`
}

// vaStatusResponse is the envelope returned by the VA status endpoint.
type vaStatusResponse struct {
	apiResponse
	VirtualAccountData vaStatusData `json:"virtualAccountData"`
}

// vaStatusData carries the VA status details. paymentFlagStatus uses the SNAP
// numeric status codes ("00" success, "03" pending, ...).
type vaStatusData struct {
	PaymentFlagStatus string `json:"paymentFlagStatus"`
	PaymentFlagReason struct {
		English   string `json:"english"`
		Indonesia string `json:"indonesia"`
	} `json:"paymentFlagReason"`
	PartnerServiceID string         `json:"partnerServiceId"`
	CustomerNo       string         `json:"customerNo"`
	VirtualAccountNo string         `json:"virtualAccountNo"`
	InquiryRequestID string         `json:"inquiryRequestId"`
	PaymentRequestID string         `json:"paymentRequestId"`
	PaidAmount       *amountPayload `json:"paidAmount"`
	TotalAmount      *amountPayload `json:"totalAmount"`
	TransactionDate  string         `json:"transactionDate"`
	TrxDateTime      string         `json:"trxDateTime"`
	ReferenceNo      string         `json:"referenceNo"`
}

// deleteVARequest is the body of POST /v1.0/transfer-va/delete-va.
type deleteVARequest struct {
	PartnerServiceID string `json:"partnerServiceId"`
	CustomerNo       string `json:"customerNo"`
	VirtualAccountNo string `json:"virtualAccountNo"`
	TrxID            string `json:"trxId"`
	AdditionalInfo   struct {
		MerchantID string `json:"merchant_id,omitempty"`
	} `json:"additionalInfo"`
}

// deleteVAResponse is the envelope returned by delete-va.
type deleteVAResponse struct {
	apiResponse
	VirtualAccountData struct {
		PartnerServiceID string `json:"partnerServiceId"`
		CustomerNo       string `json:"customerNo"`
		VirtualAccountNo string `json:"virtualAccountNo"`
		TrxID            string `json:"trxId"`
	} `json:"virtualAccountData"`
}

// --- QRIS ------------------------------------------------------------------

// createQRISRequest is the body of POST /v1.0/qr/qr-mpm-generate.
type createQRISRequest struct {
	PartnerReferenceNo string          `json:"partnerReferenceNo"`
	Amount             *amountPayload  `json:"amount,omitempty"`
	MerchantID         string          `json:"merchantId,omitempty"`
	ValidityPeriod     string          `json:"validityPeriod,omitempty"`
	AdditionalInfo     *qrisAdditional `json:"additionalInfo,omitempty"`
}

type qrisAdditional struct {
	Acquirer string `json:"acquirer,omitempty"`
}

// createQRISResponse is the envelope returned by qr-mpm-generate.
type createQRISResponse struct {
	apiResponse
	ReferenceNo        string `json:"referenceNo"`
	PartnerReferenceNo string `json:"partnerReferenceNo"`
	QRContent          string `json:"qrContent"`
	QRURL              string `json:"qrUrl"`
	QRImage            string `json:"qrImage"`
	AdditionalInfo     struct {
		Acquirer string `json:"acquirer"`
	} `json:"additionalInfo"`
}

// queryQRISRequest is the body of POST /v1.0/qr/qr-mpm-query.
type queryQRISRequest struct {
	OriginalReferenceNo        string `json:"originalReferenceNo,omitempty"`
	OriginalPartnerReferenceNo string `json:"originalPartnerReferenceNo,omitempty"`
	OriginalExternalID         string `json:"originalExternalId,omitempty"`
	MerchantID                 string `json:"merchantId,omitempty"`
	ServiceCode                string `json:"serviceCode"`
}

// queryQRISResponse is the envelope returned by qr-mpm-query.
type queryQRISResponse struct {
	apiResponse
	OriginalReferenceNo        string         `json:"originalReferenceNo"`
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo"`
	OriginalExternalID         string         `json:"originalExternalId"`
	ServiceCode                string         `json:"serviceCode"`
	LatestTransactionStatus    string         `json:"latestTransactionStatus"`
	TransactionStatusDesc      string         `json:"transactionStatusDesc"`
	PaidTime                   string         `json:"paidTime"`
	Amount                     *amountPayload `json:"amount"`
}

// cancelQRISRequest is the body of POST /v1.0/qr/qr-mpm-cancel.
type cancelQRISRequest struct {
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo,omitempty"`
	OriginalReferenceNo        string         `json:"originalReferenceNo,omitempty"`
	OriginalExternalID         string         `json:"originalExternalId,omitempty"`
	MerchantID                 string         `json:"merchantId,omitempty"`
	Reason                     string         `json:"reason,omitempty"`
	Amount                     *amountPayload `json:"amount,omitempty"`
}

// cancelQRISResponse is the envelope returned by qr-mpm-cancel.
type cancelQRISResponse struct {
	apiResponse
	OriginalPartnerReferenceNo string `json:"originalPartnerReferenceNo"`
	OriginalReferenceNo        string `json:"originalReferenceNo"`
	OriginalExternalID         string `json:"originalExternalId"`
	CancelTime                 string `json:"cancelTime"`
}

// --- E-wallet (Direct Debit) -----------------------------------------------

// createEWalletRequest is the body of POST /v1.0/debit/payment-host-to-host.
type createEWalletRequest struct {
	PartnerReferenceNo string             `json:"partnerReferenceNo"`
	ChargeToken        string             `json:"chargeToken"`
	MerchantID         string             `json:"merchantId,omitempty"`
	ValidUpTo          string             `json:"validUpTo,omitempty"`
	URLParams          []ewalletURLParam  `json:"urlParams,omitempty"`
	PayOptionDetails   []payOptionDetail  `json:"payOptionDetails"`
	AdditionalInfo     *ewalletAdditional `json:"additionalInfo,omitempty"`
}

type ewalletURLParam struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	IsDeeplink string `json:"isDeeplink"`
}

type payOptionDetail struct {
	PayMethod   string         `json:"payMethod"`
	PayOption   string         `json:"payOption"`
	TransAmount *amountPayload `json:"transAmount"`
}

type ewalletAdditional struct {
	CustomerDetails *ewalletCustomerDetails `json:"customerDetails,omitempty"`
}

// ewalletCustomerDetails is the optional payer block Midtrans accepts on
// direct-debit requests.
type ewalletCustomerDetails struct {
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// createEWalletResponse is the envelope returned by payment-host-to-host.
type createEWalletResponse struct {
	apiResponse
	ReferenceNo        string `json:"referenceNo"`
	PartnerReferenceNo string `json:"partnerReferenceNo"`
	AppRedirectURL     string `json:"appRedirectUrl"`
	WebRedirectURL     string `json:"webRedirectUrl"`
	AdditionalInfo     struct {
		PaymentType     string         `json:"paymentType"`
		GrossAmount     *amountPayload `json:"grossAmount"`
		TransactionTime string         `json:"transactionTime"`
		ValidUpTo       string         `json:"validUpTo"`
	} `json:"additionalInfo"`
}

// ewalletStatusRequest is the body of POST /v1.0/debit/status.
type ewalletStatusRequest struct {
	OriginalExternalID         string `json:"originalExternalId,omitempty"`
	OriginalReferenceNo        string `json:"originalReferenceNo,omitempty"`
	OriginalPartnerReferenceNo string `json:"originalPartnerReferenceNo,omitempty"`
	ServiceCode                string `json:"serviceCode"`
}

// ewalletStatusResponse is the envelope returned by the debit status endpoint.
type ewalletStatusResponse struct {
	apiResponse
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo"`
	OriginalReferenceNo        string         `json:"originalReferenceNo"`
	OriginalExternalID         string         `json:"originalExternalId"`
	ServiceCode                string         `json:"serviceCode"`
	LatestTransactionStatus    string         `json:"latestTransactionStatus"`
	TransAmount                *amountPayload `json:"transAmount"`
	PaidTime                   string         `json:"paidTime"`
}

// cancelEWalletRequest is the body of POST /v1.0/debit/cancel.
type cancelEWalletRequest struct {
	OriginalReferenceNo        string `json:"originalReferenceNo,omitempty"`
	OriginalExternalID         string `json:"originalExternalId,omitempty"`
	OriginalPartnerReferenceNo string `json:"originalPartnerReferenceNo,omitempty"`
}

// cancelEWalletResponse is the envelope returned by debit/cancel.
type cancelEWalletResponse struct {
	apiResponse
	OriginalReferenceNo string `json:"originalReferenceNo"`
	CancelTime          string `json:"cancelTime"`
}

// --- Notifications ----------------------------------------------------------

// vaNotification is the body of POST /v1.0/transfer-va/payment.
type vaNotification struct {
	PartnerServiceID    string         `json:"partnerServiceId"`
	CustomerNo          string         `json:"customerNo"`
	VirtualAccountNo    string         `json:"virtualAccountNo"`
	VirtualAccountName  string         `json:"virtualAccountName"`
	VirtualAccountEmail string         `json:"virtualAccountEmail"`
	VirtualAccountPhone string         `json:"virtualAccountPhone"`
	TrxID               string         `json:"trxId"`
	PaymentRequestID    string         `json:"paymentRequestId"`
	PaidAmount          *amountPayload `json:"paidAmount"`
	TotalAmount         *amountPayload `json:"totalAmount"`
	ReferenceNo         string         `json:"referenceNo"`
	AdditionalInfo      struct {
		Bank              string `json:"bank"`
		PaymentFlagStatus string `json:"paymentFlagStatus"`
		MerchantID        string `json:"merchantId"`
		PaidTime          string `json:"paidTime"`
	} `json:"additionalInfo"`
}

// qrisNotification is the body of POST /v1.0/qr/qr-mpm-notify.
type qrisNotification struct {
	OriginalReferenceNo        string         `json:"originalReferenceNo"`
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo"`
	LatestTransactionStatus    string         `json:"latestTransactionStatus"`
	TransactionStatusDesc      string         `json:"transactionStatusDesc"`
	Amount                     *amountPayload `json:"amount"`
	ExternalStoreID            string         `json:"externalStoreID"`
}

// ewalletNotification is the body of POST /v1.0/debit/notify.
type ewalletNotification struct {
	CreatedTime                string         `json:"createdTime"`
	FinishedTime               string         `json:"finishedTime"`
	OriginalReferenceNo        string         `json:"originalReferenceNo"`
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo"`
	OriginalExternalID         string         `json:"originalExternalId"`
	LatestTransactionStatus    string         `json:"latestTransactionStatus"`
	TransactionStatusDesc      string         `json:"transactionStatusDesc"`
	Amount                     *amountPayload `json:"amount"`
}

// rawNotification is used to peek at the payload shape before deciding which
// concrete notification type to unmarshal into. Every field is optional; the
// presence of one of them selects the notification family.
type rawNotification struct {
	VirtualAccountNo           string `json:"virtualAccountNo"`
	PartnerServiceID           string `json:"partnerServiceId"`
	OriginalPartnerReferenceNo string `json:"originalPartnerReferenceNo"`
	OriginalReferenceNo        string `json:"originalReferenceNo"`
	OriginalExternalID         string `json:"originalExternalId"`
	LatestTransactionStatus    string `json:"latestTransactionStatus"`
	ExternalStoreID            string `json:"externalStoreID"`
	CreatedTime                string `json:"createdTime"`
	FinishedTime               string `json:"finishedTime"`
	AdditionalInfo             struct {
		// QRIS and UserPaymentDetails discriminate QRIS from e-wallet
		// notifications. They are kept raw so only their presence matters.
		QRIS               json.RawMessage `json:"qris"`
		UserPaymentDetails json.RawMessage `json:"userPaymentDetails"`
	} `json:"additionalInfo"`
}
