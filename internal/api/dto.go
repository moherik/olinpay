package api

import "github.com/moherik/openpay.id/pkg/provider"

// createPaymentRequest is the body of POST /v1/payments.
type createPaymentRequest struct {
	// ReferenceID must be unique per merchant; it is the idempotency key.
	ReferenceID string `json:"reference_id"`
	// Amount is in the smallest currency unit (rupiah for IDR).
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	// Method is one of the unified methods (virtual_account, qris, ewallet,
	// retail_outlet, card, paylater, direct_debit).
	Method string `json:"method"`
	// Channel optionally selects a specific instrument (e.g. "BCA", "GOPAY").
	Channel string `json:"channel"`
	// Provider optionally forces a provider, bypassing routing.
	Provider string `json:"provider"`
	// Description is shown to the payer.
	Description string `json:"description"`
	// ExpirySeconds overrides the default payment lifetime.
	ExpirySeconds int               `json:"expiry_seconds"`
	Customer      customerDTO       `json:"customer"`
	Items         []itemDTO         `json:"items"`
	Metadata      map[string]string `json:"metadata"`
}

// customerDTO is the payer identity.
type customerDTO struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Reference string `json:"reference"`
}

func (c customerDTO) toProvider() provider.Customer {
	return provider.Customer{
		Name:      c.Name,
		Email:     c.Email,
		Phone:     c.Phone,
		Reference: c.Reference,
	}
}

// itemDTO is a single line item.
type itemDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Quantity int    `json:"quantity"`
}

func (i itemDTO) toProvider() provider.Item {
	return provider.Item{ID: i.ID, Name: i.Name, Price: i.Price, Quantity: i.Quantity}
}

// cancelPaymentRequest is the body of POST /v1/payments/{id}/cancel.
type cancelPaymentRequest struct {
	// Reason is "cancel" (default) or "expire".
	Reason string `json:"reason"`
}
