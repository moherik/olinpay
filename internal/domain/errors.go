package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors used across the core engine, repositories and API layer.
// The API layer maps these onto HTTP status codes.
var (
	// ErrNotFound means the requested entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict means a uniqueness constraint was violated.
	ErrConflict = errors.New("conflict")
	// ErrValidation means the caller supplied invalid input.
	ErrValidation = errors.New("validation")
	// ErrUnauthorized means the API credential is missing or invalid.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden means the credential is valid but not allowed here.
	ErrForbidden = errors.New("forbidden")
	// ErrNoRoute means no provider could be selected for the request.
	ErrNoRoute = errors.New("no provider route available")
	// ErrUnprocessable means the request is understood but cannot be applied
	// to the current state (e.g. cancelling a paid transaction).
	ErrUnprocessable = errors.New("unprocessable")
)

// ValidationError carries field-level detail for invalid input.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Unwrap makes ValidationError satisfy errors.Is(err, ErrValidation).
func (e *ValidationError) Unwrap() error { return ErrValidation }

// Invalid builds a [*ValidationError], a convenience for input checks.
func Invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}
