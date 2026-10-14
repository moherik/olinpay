package provider

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by the contract. Implementations should wrap these
// (via %w or [Error.Unwrap]) where they apply so the core engine can react.
var (
	// ErrUnsupportedOperation means the provider cannot perform the requested
	// operation (e.g. Cancel on a gateway without a void endpoint).
	ErrUnsupportedOperation = errors.New("provider: operation not supported")
	// ErrNotFound means the gateway reports no such transaction.
	ErrNotFound = errors.New("provider: transaction not found")
	// ErrInvalidSignature means an inbound webhook failed authentication.
	ErrInvalidSignature = errors.New("provider: invalid webhook signature")
	// ErrInvalidRequest means the request is malformed for this gateway.
	ErrInvalidRequest = errors.New("provider: invalid request")
	// ErrDuplicateReference means the gateway already knows the reference.
	ErrDuplicateReference = errors.New("provider: duplicate reference")
	// ErrGateway means the gateway returned an error (network, 5xx, timeout).
	ErrGateway = errors.New("provider: gateway error")
	// ErrUnauthorized means the gateway rejected our credentials.
	ErrUnauthorized = errors.New("provider: unauthorized")
)

// Error is the structured error every provider should return for failures that
// originate from the gateway. It keeps the gateway's own error code and message
// alongside a wrapped sentinel so callers can use [errors.Is].
type Error struct {
	// Provider is the provider name that produced the error.
	Provider string
	// Op is the operation that failed: "CreatePayment", "Inquiry", ...
	Op string
	// Code is the gateway's error code, when available.
	Code string
	// Message is a human readable description.
	Message string
	// HTTPStatus is the gateway HTTP status, when available.
	HTTPStatus int
	// Err is the wrapped sentinel/cause.
	Err error
}

// Error implements the error interface.
func (e *Error) Error() string {
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	s := fmt.Sprintf("provider %s: %s failed", e.Provider, e.Op)
	if e.Code != "" {
		s += fmt.Sprintf(" [%s]", e.Code)
	}
	if msg != "" {
		s += ": " + msg
	}
	return s
}

// Unwrap exposes the wrapped cause for errors.Is / errors.As.
func (e *Error) Unwrap() error { return e.Err }

// NewError builds a [*Error] with a wrapped sentinel.
func NewError(providerName, op, code, message string, cause error) *Error {
	return &Error{
		Provider: providerName,
		Op:       op,
		Code:     code,
		Message:  message,
		Err:      cause,
	}
}

// WrapError wraps cause into a [*Error] if it is not already one, keeping the
// original chain intact.
func WrapError(providerName, op string, cause error) error {
	if cause == nil {
		return nil
	}
	var pe *Error
	if errors.As(cause, &pe) {
		return cause
	}
	return &Error{Provider: providerName, Op: op, Message: cause.Error(), Err: cause}
}
