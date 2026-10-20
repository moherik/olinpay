package repository

import (
	"context"

	"github.com/moherik/openpay.id/internal/domain"
)

type ActivityFilter struct {
	TransactionID string
	MerchantID    string
	Environment   string
	ReferenceID   string
	Limit         int
}

type TransactionActivityRepository interface {
	Create(context.Context, *domain.TransactionActivity) error
	List(context.Context, ActivityFilter) ([]*domain.TransactionActivity, error)
}
