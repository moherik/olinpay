package doku

import "encoding/json"

// This file mirrors the payload shapes published in the DOKU SNAP API
// documentation (https://developers.doku.com). Field names and JSON tags are
// kept as-is so the mapping stays obvious; unified translation happens in
// doku.go.

// amountPayload is the {"value","currency"} object used by every DOKU
// endpoint, both on requests and on responses.
type amountPayload struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

// apiResponse is embedded into every response envelope.
type apiResponse struct {
	ResponseCode    string `json:"responseCode"`
	ResponseMessage string `json:"responseMessage"`
}

// --- Get Token -------------------------------------------------------------

type getTokenRequest struct {
	GrantType string `json:"grantType"`
}

type getTokenResponse struct {
	apiResponse
	AccessToken string `json:"accessToken"`
	TokenType   string `json:"tokenType"`
	ExpiresIn   int    `json:"expiresIn"`
}

// --- Virtual Account -------------------------------------------------------

// createVARequest is the body of POST .../transfer-va/create-va.
type createVARequest struct {
	PartnerServiceID      string         `json:"partnerServiceId"`
	CustomerNo            string         `json:"customerNo,omitempty"`
	VirtualAccountNo      string         `json:"virtualAccountNo,omitempty"`
	VirtualAccountName    string         `json:"virtualAccountName"`
	VirtualAccountEmail   string         `json:"virtualAccountEmail,omitempty"`
	VirtualAccountPhone   string         `json:"virtualAccountPhone,omitempty"`
	TrxID                 string         `json:"trxId"`
	VirtualAccountTrxType string         `json:"virtualAccountTrxType"`
	TotalAmount           *amountPayload `json:"totalAmount,omitempty"`
	ExpiredDate           string         `json:"expiredDate,omitempty"`
	AdditionalInfo        vaAdditional   `json:"additionalInfo"`
}

type vaAdditional struct {
	Channel              string         `json:"channel"`
	VirtualAccountConfig map[string]any `json:"virtualAccountConfig,omitempty"`
}

// createVAResponse is the envelope returned by create-va.
type createVAResponse struct {
	apiResponse
	VirtualAccountData vaData `json:"virtualAccountData"`
}

// vaData carries the virtual account details returned by DOKU.
type vaData struct {
	PartnerServiceID   string         `json:"partnerServiceId"`
	CustomerNo         string         `json:"customerNo"`
	VirtualAccountNo   string         `json:"virtualAccountNo"`
	VirtualAccountName string         `json:"virtualAccountName"`
	TrxID              string         `json:"trxId"`
	TotalAmount        *amountPayload `json:"totalAmount"`
	ExpiredDate        string         `json:"expiredDate"`
	AdditionalInfo     struct {
		Channel      string `json:"channel"`
		HowToPayPage string `json:"howToPayPage,omitempty"`
	} `json:"additionalInfo"`
}

// vaStatusRequest is the body of POST /orders/v1.0/transfer-va/status.
type vaStatusRequest struct {
	PartnerServiceID string   `json:"partnerServiceId"`
	CustomerNo       string   `json:"customerNo"`
	VirtualAccountNo string   `json:"virtualAccountNo"`
	InquiryRequestID string   `json:"inquiryRequestId,omitempty"`
	PaymentRequestID string   `json:"paymentRequestId,omitempty"`
	AdditionalInfo   struct{} `json:"additionalInfo"`
}

// vaStatusResponse is the envelope returned by the VA status endpoint.
type vaStatusResponse struct {
	apiResponse
	VirtualAccountData vaStatusData `json:"virtualAccountData"`
	AdditionalInfo     struct {
		TrxID    string `json:"trxId"`
		Acquirer struct {
			ID string `json:"id"`
		} `json:"acquirer"`
	} `json:"additionalInfo"`
}

// vaStatusData carries the VA status details.
type vaStatusData struct {
	PaymentFlagReason struct {
		English   string `json:"english"`
		Indonesia string `json:"indonesia"`
	} `json:"paymentFlagReason"`
	PartnerServiceID  string         `json:"partnerServiceId"`
	CustomerNo        string         `json:"customerNo"`
	VirtualAccountNo  string         `json:"virtualAccountNo"`
	PaidAmount        *amountPayload `json:"paidAmount"`
	PaymentFlagStatus string         `json:"paymentFlagStatus"`
	PaymentRequestID  string         `json:"paymentRequestId"`
	TransactionDate   string         `json:"transactionDate"`
}

// deleteVARequest is the body of DELETE .../transfer-va/delete-va.
type deleteVARequest struct {
	PartnerServiceID string       `json:"partnerServiceId"`
	CustomerNo       string       `json:"customerNo"`
	VirtualAccountNo string       `json:"virtualAccountNo"`
	TrxID            string       `json:"trxId"`
	AdditionalInfo   vaAdditional `json:"additionalInfo"`
}

// deleteVAResponse is the envelope returned by delete-va.
type deleteVAResponse struct {
	apiResponse
	VirtualAccountData struct {
		PartnerServiceID string `json:"partnerServiceId"`
		CustomerNo       string `json:"customerNo"`
		VirtualAccountNo string `json:"virtualAccountNo"`
	} `json:"virtualAccountData"`
}

// --- QRIS ------------------------------------------------------------------

// createQRISRequest is the body of POST /snap-adapter/b2b/v1.0/qr/qr-mpm-generate.
type createQRISRequest struct {
	PartnerReferenceNo string         `json:"partnerReferenceNo"`
	Amount             *amountPayload `json:"amount"`
	MerchantID         string         `json:"merchantId"`
	TerminalID         string         `json:"terminalId"`
	ValidityPeriod     string         `json:"validityPeriod,omitempty"`
	AdditionalInfo     qrisAdditional `json:"additionalInfo"`
}

type qrisAdditional struct {
	PostalCode string `json:"postalCode"`
	FeeType    int    `json:"feeType"`
}

// createQRISResponse is the envelope returned by qr-mpm-generate.
type createQRISResponse struct {
	apiResponse
	ReferenceNo        string `json:"referenceNo"`
	PartnerReferenceNo string `json:"partnerReferenceNo"`
	QRContent          string `json:"qrContent"`
	TerminalID         string `json:"terminalId"`
	AdditionalInfo     struct {
		ValidityPeriod string `json:"validityPeriod"`
	} `json:"additionalInfo"`
}

// queryQRISRequest is the body of POST /snap-adapter/b2b/v1.0/qr/qr-mpm-query.
type queryQRISRequest struct {
	OriginalReferenceNo        string `json:"originalReferenceNo"`
	OriginalPartnerReferenceNo string `json:"originalPartnerReferenceNo"`
	ServiceCode                string `json:"serviceCode"`
	MerchantID                 string `json:"merchantId"`
}

// queryQRISResponse is the envelope returned by qr-mpm-query.
type queryQRISResponse struct {
	apiResponse
	OriginalReferenceNo        string         `json:"originalReferenceNo"`
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo"`
	ServiceCode                string         `json:"serviceCode"`
	LatestTransactionStatus    string         `json:"latestTransactionStatus"`
	TransactionStatusDesc      string         `json:"transactionStatusDesc"`
	PaidTime                   string         `json:"paidTime"`
	Amount                     *amountPayload `json:"amount"`
}

// cancelQRISRequest is the body of POST /snap-adapter/b2b/v1.0/qr/qr-expire.
type cancelQRISRequest struct {
	PartnerReferenceNo string `json:"partnerReferenceNo"`
	ReferenceNo        string `json:"referenceNo"`
	MerchantID         string `json:"merchantId"`
	Reason             string `json:"reason,omitempty"`
}

// cancelQRISResponse is the envelope returned by qr-expire.
type cancelQRISResponse struct {
	apiResponse
	PartnerReferenceNo string `json:"partnerReferenceNo"`
	ReferenceNo        string `json:"referenceNo"`
	ExpiredDate        string `json:"expiredDate"`
}

// --- E-wallet (Direct Debit) -----------------------------------------------

// createEWalletRequest is the body of POST /direct-debit/core/v{1,2}/debit/payment-host-to-host.
type createEWalletRequest struct {
	PartnerReferenceNo string            `json:"partnerReferenceNo"`
	ValidUpTo          string            `json:"validUpTo,omitempty"`
	Amount             *amountPayload    `json:"amount"`
	URLParam           *ewalletURLParam  `json:"urlParam,omitempty"`
	URLParams          []ewalletURLParam `json:"urlParams,omitempty"`
	AdditionalInfo     ewalletAdditional `json:"additionalInfo"`
}

type ewalletURLParam struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	IsDeepLink string `json:"isDeepLink"`
}

type ewalletAdditional struct {
	Channel    string `json:"channel"`
	OrderTitle string `json:"orderTitle,omitempty"`
}

// createEWalletResponse is the envelope returned by payment-host-to-host.
type createEWalletResponse struct {
	apiResponse
	ReferenceNo        string `json:"referenceNo"`
	PartnerReferenceNo string `json:"partnerReferenceNo"`
	WebRedirectURL     string `json:"webRedirectUrl"`
}

// ewalletStatusRequest is the body of POST /orders/v1.0/debit/status.
type ewalletStatusRequest struct {
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo,omitempty"`
	OriginalReferenceNo        string         `json:"originalReferenceNo,omitempty"`
	OriginalExternalID         string         `json:"originalExternalId,omitempty"`
	ServiceCode                string         `json:"serviceCode"`
	TransactionDate            string         `json:"transactionDate,omitempty"`
	Amount                     *amountPayload `json:"amount,omitempty"`
	MerchantID                 string         `json:"merchantId,omitempty"`
	AdditionalInfo             struct {
		Channel string `json:"channel"`
	} `json:"additionalInfo"`
}

// ewalletStatusResponse is the envelope returned by the debit status endpoint.
type ewalletStatusResponse struct {
	apiResponse
	OriginalPartnerReferenceNo string `json:"originalPartnerReferenceNo"`
	OriginalReferenceNo        string `json:"originalReferenceNo"`
	ApprovalCode               string `json:"approvalCode"`
	OriginalExternalID         string `json:"originalExternalId"`
	ServiceCode                string `json:"serviceCode"`
	LatestTransactionStatus    string `json:"latestTransactionStatus"`
	TransactionStatusDesc      string `json:"transactionStatusDesc"`
}

// --- Notifications ----------------------------------------------------------

// vaNotification is the body of POST /v1/transfer-va/payment.
type vaNotification struct {
	PartnerServiceID   string         `json:"partnerServiceId"`
	CustomerNo         string         `json:"customerNo"`
	VirtualAccountNo   string         `json:"virtualAccountNo"`
	VirtualAccountName string         `json:"virtualAccountName"`
	TrxID              string         `json:"trxId"`
	PaymentRequestID   string         `json:"paymentRequestId"`
	PaidAmount         *amountPayload `json:"paidAmount"`
	TotalAmount        *amountPayload `json:"totalAmount"`
	TransactionDate    string         `json:"transactionDate"`
	AdditionalInfo     struct {
		Channel string `json:"channel"`
	} `json:"additionalInfo"`
}

// ddNotification is the body of POST /v1.0/debit/notify.
type ddNotification struct {
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo"`
	OriginalReferenceNo        string         `json:"originalReferenceNo"`
	OriginalExternalID         string         `json:"originalExternalId"`
	LatestTransactionStatus    string         `json:"latestTransactionStatus"`
	TransactionStatusDesc      string         `json:"transactionStatusDesc"`
	Amount                     *amountPayload `json:"amount"`
	AdditionalInfo             struct {
		ChannelID string `json:"channelId"`
	} `json:"additionalInfo"`
}

// rawNotification is used to peek at the payload shape before deciding which
// concrete notification type to unmarshal into.
type rawNotification struct {
	VirtualAccountNo           string         `json:"virtualAccountNo"`
	PaidAmount                 *amountPayload `json:"paidAmount"`
	PartnerServiceID           string         `json:"partnerServiceId"`
	LatestTransactionStatus    string         `json:"latestTransactionStatus"`
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo"`
}

// ensure json is used (Raw fields in responses).
var _ = json.RawMessage(nil)
