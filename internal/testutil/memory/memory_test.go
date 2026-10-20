package memory_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/testutil/memory"
)

// TestAdminUserCreateFirstConcurrent catches an implementation that checks for
// an empty store separately from inserting the first console user.
func TestAdminUserCreateFirstConcurrent(t *testing.T) {
	store := memory.NewStore()
	users := store.AdminUsers()
	start := make(chan struct{})
	errs := make(chan error, 16)

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			now := time.Now()
			errs <- users.CreateFirst(context.Background(), &domain.AdminUser{
				ID:           fmt.Sprintf("usr_%d", i),
				Email:        fmt.Sprintf("owner-%d@example.test", i),
				Name:         "Owner",
				PasswordHash: "hash",
				Role:         domain.RoleOwner,
				Status:       domain.AdminUserActive,
				CreatedAt:    now,
				UpdatedAt:    now,
			})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	var created, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			created++
		case errors.Is(err, domain.ErrConflict):
			conflicts++
		default:
			t.Fatalf("CreateFirst error = %v, want nil or ErrConflict", err)
		}
	}
	if created != 1 || conflicts != 15 {
		t.Fatalf("CreateFirst results: %d created, %d conflicts; want 1 and 15", created, conflicts)
	}
	count, err := users.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("Count = %d, want 1", count)
	}
}

func TestInvalidMerchantCreateIsRejected(t *testing.T) {
	merchants := memory.NewStore().Merchants()
	err := merchants.Create(context.Background(), &domain.Merchant{ID: "mch_legacy"})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Create error = %v, want ErrValidation", err)
	}

	got, err := merchants.List(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("stored merchants = %d, want 0", len(got))
	}
}
