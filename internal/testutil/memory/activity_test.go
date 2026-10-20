package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/internal/testutil/memory"
)

func TestActivityRepositoryListsScopedChronologicalRows(t *testing.T) {
	s := memory.NewStore()
	repo := s.Activities()
	base := time.Unix(100, 0)
	rows := []*domain.TransactionActivity{
		{ID: "a2", TransactionID: "tx-1", MerchantID: "m-1", ReferenceID: "ref-1", CreatedAt: base.Add(time.Second)},
		{ID: "a1", TransactionID: "tx-1", MerchantID: "m-1", ReferenceID: "ref-1", CreatedAt: base},
		{ID: "other", TransactionID: "tx-2", MerchantID: "m-2", ReferenceID: "ref-2", CreatedAt: base},
	}
	for _, row := range rows {
		if err := repo.Create(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.List(context.Background(), repository.ActivityFilter{TransactionID: "tx-1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a1" || got[1].ID != "a2" {
		t.Fatalf("rows = %#v, want a1 then a2", got)
	}
}
