// Package provider defines the public contract that every payment gateway
// integration ("provider") in openpay.id must satisfy, together with the
// unified vocabulary (statuses, methods, actions) shared by all providers.
//
// A provider is a thin adapter. It translates the unified request types into a
// gateway's own API calls and maps the gateway's payloads back into the unified
// response/webhook types. A provider never talks to the database, to the core
// engine, or to any other provider directly.
//
// Third-party integrations only need to import this package:
//
//	import "github.com/moherik/openpay.id/pkg/provider"
//
// To add a new gateway:
//
//  1. Create a type that implements [Provider].
//  2. Create a [Factory] and register it with [Register] from an init function.
//  3. Enable it at runtime through configuration (see the project README).
package provider

// Status is the unified, gateway-agnostic lifecycle status of a payment.
// Every provider MUST map its own status codes onto exactly one of these
// values so that the core engine can reason about transactions uniformly.
type Status string

const (
	// StatusPending means the payment exists and is awaiting settlement by
	// the end customer (e.g. a VA that has been issued but not yet paid).
	StatusPending Status = "pending"
	// StatusPaid means funds were received / the transaction succeeded.
	StatusPaid Status = "paid"
	// StatusExpired means the payment window elapsed without payment.
	StatusExpired Status = "expired"
	// StatusCancelled means the transaction was cancelled before settlement.
	StatusCancelled Status = "cancelled"
	// StatusFailed means the gateway rejected or failed the transaction.
	StatusFailed Status = "failed"
	// StatusRefunded means the paid transaction was refunded.
	StatusRefunded Status = "refunded"
	// StatusUnknown is used when a gateway returns a code we cannot map yet.
	// It is never used for a newly created successful request.
	StatusUnknown Status = "unknown"
)

// AllStatuses lists every valid status, useful for validation and docs.
var AllStatuses = []Status{
	StatusPending,
	StatusPaid,
	StatusExpired,
	StatusCancelled,
	StatusFailed,
	StatusRefunded,
	StatusUnknown,
}

// IsValid reports whether s is one of the defined statuses.
func (s Status) IsValid() bool {
	for _, v := range AllStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// IsFinal reports whether s represents a terminal state that must not
// transition back into a non-final state.
func (s Status) IsFinal() bool {
	switch s {
	case StatusPaid, StatusExpired, StatusCancelled, StatusFailed, StatusRefunded:
		return true
	default:
		return false
	}
}

// String implements fmt.Stringer.
func (s Status) String() string { return string(s) }

// Method is the unified payment method category, independent of gateway.
type Method string

const (
	// MethodVirtualAccount covers bank virtual accounts (BCA VA, Mandiri VA, ...).
	MethodVirtualAccount Method = "virtual_account"
	// MethodQRIS covers EMVCo QRIS (static and dynamic).
	MethodQRIS Method = "qris"
	// MethodEWallet covers e-wallets (GoPay, OVO, DANA, ShopeePay, ...).
	MethodEWallet Method = "ewallet"
	// MethodRetailOutlet covers over-the-counter channels (Alfamart, Indomaret, ...).
	MethodRetailOutlet Method = "retail_outlet"
	// MethodCard covers card payments (credit/debit) when the gateway handles them.
	MethodCard Method = "card"
	// MethodPaylater covers pay-later / installment products.
	MethodPaylater Method = "paylater"
	// MethodDirectDebit covers account debiting (bank transfer / direct debit).
	MethodDirectDebit Method = "direct_debit"
)

// AllMethods lists every unified method.
var AllMethods = []Method{
	MethodVirtualAccount,
	MethodQRIS,
	MethodEWallet,
	MethodRetailOutlet,
	MethodCard,
	MethodPaylater,
	MethodDirectDebit,
}

// IsValid reports whether m is one of the defined methods.
func (m Method) IsValid() bool {
	for _, v := range AllMethods {
		if m == v {
			return true
		}
	}
	return false
}

// String implements fmt.Stringer.
func (m Method) String() string { return string(m) }
