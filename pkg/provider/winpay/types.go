package winpay

// This file mirrors the payload shapes published in the Winpay SNAP API
// documentation (https://docs.winpay.id). Field names and types are kept as-is
// so the mapping stays obvious; unified translation happens in winpay.go.

// amountPayload is the {"value","currency"} object used by every Winpay
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

// contractAdditional is the {"contractId"} object used by the query/cancel
// endpoints.
type contractAdditional struct {
	ContractID string `json:"contractId"`
}

// --- Virtual Account -------------------------------------------------------

type createVARequest struct {
	CustomerNo            string         `json:"customerNo,omitempty"`
	VirtualAccountName    string         `json:"virtualAccountName"`
	TrxID                 string         `json:"trxId"`
	TotalAmount           *amountPayload `json:"totalAmount,omitempty"`
	VirtualAccountTrxType string         `json:"virtualAccountTrxType"`
	ExpiredDate           string         `json:"expiredDate,omitempty"`
	AdditionalInfo        vaAdditional   `json:"additionalInfo"`
}

type vaAdditional struct {
	Channel string `json:"channel"`
}

// vaRequestAdditional carries the fields the VA inquiry/status/delete endpoints
// expect inside "additionalInfo".
type vaRequestAdditional struct {
	ContractID string `json:"contractId"`
	Channel    string `json:"channel,omitempty"`
	TrxID      string `json:"trxId,omitempty"`
}

type vaStatusRequest struct {
	VirtualAccountNo string              `json:"virtualAccountNo"`
	AdditionalInfo   vaRequestAdditional `json:"additionalInfo"`
}

type vaInquiryRequest struct {
	TrxID          string              `json:"trxId"`
	AdditionalInfo vaRequestAdditional `json:"additionalInfo"`
}

type vaDeleteRequest struct {
	VirtualAccountNo string              `json:"virtualAccountNo"`
	TrxID            string              `json:"trxId"`
	AdditionalInfo   vaRequestAdditional `json:"additionalInfo"`
}

type vaResponseAdditional struct {
	Channel    string `json:"channel"`
	ContractID string `json:"contractId"`
}

// vaData is the "virtualAccountData" object returned by create-va and
// inquiry-va.
type vaData struct {
	PartnerServiceID   string               `json:"partnerServiceId"`
	CustomerNo         string               `json:"customerNo"`
	VirtualAccountNo   string               `json:"virtualAccountNo"`
	VirtualAccountName string               `json:"virtualAccountName"`
	TrxID              string               `json:"trxId"`
	TotalAmount        *amountPayload       `json:"totalAmount"`
	ExpiredDate        string               `json:"expiredDate"`
	AdditionalInfo     vaResponseAdditional `json:"additionalInfo"`
}

type createVAResponse struct {
	apiResponse
	VirtualAccountData vaData `json:"virtualAccountData"`
}

type inquiryVAResponse struct {
	apiResponse
	VirtualAccountData vaData `json:"virtualAccountData"`
}

// vaStatusData is the "virtualAccountData" object returned by the status
// endpoint, which adds the payment flag.
type vaStatusData struct {
	VirtualAccountNo   string         `json:"virtualAccountNo"`
	VirtualAccountName string         `json:"virtualAccountName"`
	PaymentFlagStatus  string         `json:"paymentFlagStatus"`
	TransactionDate    string         `json:"transactionDate"`
	ReferenceNo        string         `json:"referenceNo"`
	TotalAmount        *amountPayload `json:"totalAmount"`
}

type vaStatusResponse struct {
	apiResponse
	VirtualAccountData vaStatusData `json:"virtualAccountData"`
}

type deleteVAResponse struct {
	apiResponse
	VirtualAccountData struct {
		TrxID string `json:"trxId"`
	} `json:"virtualAccountData"`
	AdditionalInfo vaResponseAdditional `json:"additionalInfo"`
}

// --- QRIS ------------------------------------------------------------------

type createQRISRequest struct {
	PartnerReferenceNo string         `json:"partnerReferenceNo"`
	Amount             amountPayload  `json:"amount"`
	ValidityPeriod     string         `json:"validityPeriod,omitempty"`
	TerminalID         string         `json:"terminalId,omitempty"`
	AdditionalInfo     qrisAdditional `json:"additionalInfo"`
}

type qrisAdditional struct {
	IsStatic bool `json:"isStatic"`
}

type qrisQueryRequest struct {
	OriginalPartnerReferenceNo string             `json:"originalPartnerReferenceNo"`
	ServiceCode                string             `json:"serviceCode"`
	AdditionalInfo             contractAdditional `json:"additionalInfo"`
}

type qrisCancelRequest struct {
	OriginalPartnerReferenceNo string             `json:"originalPartnerReferenceNo"`
	Reason                     string             `json:"reason"`
	AdditionalInfo             contractAdditional `json:"additionalInfo"`
}

type qrisResponseAdditional struct {
	ContractID string `json:"contractId"`
	ExpiredAt  string `json:"expiredAt"`
	IsStatic   bool   `json:"isStatic"`
}

type createQRISResponse struct {
	apiResponse
	PartnerReferenceNo string                 `json:"partnerReferenceNo"`
	QRContent          *string                `json:"qrContent"`
	QRURL              string                 `json:"qrUrl"`
	TerminalID         string                 `json:"terminalId"`
	AdditionalInfo     qrisResponseAdditional `json:"additionalInfo"`
}

type qrisQueryResponse struct {
	apiResponse
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo"`
	ServiceCode                string         `json:"serviceCode"`
	LatestTransactionStatus    string         `json:"latestTransactionStatus"`
	TransactionStatusDesc      string         `json:"transactionStatusDesc"`
	PaidTime                   string         `json:"paidTime"`
	Amount                     *amountPayload `json:"amount"`
	FeeAmount                  *amountPayload `json:"feeAmount"`
	TerminalID                 string         `json:"terminalId"`
	AdditionalInfo             struct {
		Issuer   string `json:"issuer"`
		RRN      string `json:"rrn"`
		BuyerRef string `json:"buyerReff"`
	} `json:"additionalInfo"`
}

type cancelQRISResponse struct {
	apiResponse
	OriginalPartnerReferenceNo string             `json:"originalPartnerReferenceNo"`
	CancelTime                 string             `json:"cancelTime"`
	AdditionalInfo             contractAdditional `json:"additionalInfo"`
}

// --- eWallet ---------------------------------------------------------------

type createEWalletRequest struct {
	PartnerReferenceNo string            `json:"partnerReferenceNo"`
	Amount             amountPayload     `json:"amount"`
	URLParam           []urlParam        `json:"urlParam"`
	ValidUpTo          string            `json:"validUpTo"`
	AdditionalInfo     ewalletAdditional `json:"additionalInfo"`
}

type urlParam struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	IsDeeplink string `json:"isDeeplink"`
}

type ewalletAdditional struct {
	Channel       string `json:"channel"`
	CustomerPhone string `json:"customerPhone"`
	CustomerName  string `json:"customerName"`
}

type ewalletRequestAdditional struct {
	ContractID string `json:"contractId"`
	Channel    string `json:"channel,omitempty"`
}

type ewalletStatusRequest struct {
	OriginalPartnerReferenceNo string                   `json:"originalPartnerReferenceNo"`
	AdditionalInfo             ewalletRequestAdditional `json:"additionalInfo"`
}

type ewalletCancelRequest struct {
	OriginalPartnerReferenceNo string                   `json:"originalPartnerReferenceNo"`
	Reason                     string                   `json:"reason"`
	AdditionalInfo             ewalletRequestAdditional `json:"additionalInfo"`
}

type ewalletResponseAdditional struct {
	ContractID   string `json:"contractId"`
	ExpiredTime  string `json:"expiredTime"`
	Channel      string `json:"channel"`
	Instructions []any  `json:"instructions"`
}

type createEWalletResponse struct {
	apiResponse
	PartnerReferenceNo string                    `json:"partnerReferenceNo"`
	AppRedirectURL     string                    `json:"appRedirectUrl"`
	WebRedirectURL     string                    `json:"webRedirectUrl"`
	AdditionalInfo     ewalletResponseAdditional `json:"additionalInfo"`
}

type ewalletStatusResponse struct {
	apiResponse
	OriginalPartnerReferenceNo string             `json:"originalPartnerReferenceNo"`
	ServiceCode                string             `json:"serviceCode"`
	LatestTransactionStatus    string             `json:"latestTransactionStatus"`
	AdditionalInfo             contractAdditional `json:"additionalInfo"`
}

type cancelEWalletResponse struct {
	apiResponse
	OriginalPartnerReferenceNo string             `json:"originalPartnerReferenceNo"`
	AdditionalInfo             contractAdditional `json:"additionalInfo"`
}

// --- inbound callbacks -----------------------------------------------------

// callbackPayload is the union of the three documented callback shapes. Which
// product sent the callback is inferred from which fields are populated:
// "paidAmount" means a Virtual Account payment, "latestTransactionStatus" means
// QRIS or eWallet (disambiguated by the channel code).
type callbackPayload struct {
	// Virtual Account payment notification.
	PartnerServiceID   string         `json:"partnerServiceId"`
	CustomerNo         string         `json:"customerNo"`
	VirtualAccountNo   string         `json:"virtualAccountNo"`
	VirtualAccountName string         `json:"virtualAccountName"`
	TrxID              string         `json:"trxId"`
	PaymentRequestID   string         `json:"paymentRequestId"`
	PaidAmount         *amountPayload `json:"paidAmount"`
	TrxDateTime        string         `json:"trxDateTime"`
	// ReferenceNo is emitted as a bare number by some Winpay endpoints, so it
	// is kept loosely typed; openpay.id does not use it for correlation.
	ReferenceNo any `json:"referenceNo"`

	// QRIS / eWallet notification.
	OriginalReferenceNo        string         `json:"originalReferenceNo"`
	OriginalPartnerReferenceNo string         `json:"originalPartnerReferenceNo"`
	LatestTransactionStatus    string         `json:"latestTransactionStatus"`
	TransactionStatusDesc      string         `json:"transactionStatusDesc"`
	Amount                     *amountPayload `json:"amount"`
	PaidTime                   string         `json:"paidTime"`

	AdditionalInfo struct {
		Channel    string `json:"channel"`
		ContractID string `json:"contractId"`
		Issuer     string `json:"issuer"`
		RRN        string `json:"rrn"`
		BuyerRef   string `json:"buyerRef"`
	} `json:"additionalInfo"`
}
