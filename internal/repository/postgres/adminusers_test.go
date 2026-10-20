package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/migrate"
	"github.com/moherik/openpay.id/internal/repository/postgres"
)

// TestAdminUserCreateFirstConcurrentPostgres verifies the transaction-level
// guard against multiple first owners. It requires an explicitly named test
// database so it cannot be run against the normal development database.
func TestAdminUserCreateFirstConcurrentPostgres(t *testing.T) {
	databaseURL := os.Getenv("OPENPAY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPENPAY_TEST_DATABASE_URL is not set")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parsing OPENPAY_TEST_DATABASE_URL: %v", err)
	}
	if !strings.Contains(strings.ToLower(config.ConnConfig.Database), "test") {
		t.Fatalf("OPENPAY_TEST_DATABASE_URL database %q must contain %q", config.ConnConfig.Database, "test")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("connecting to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locating migrations directory")
	}
	migrationsDir := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../../migrations"))
	if err := migrate.Run(t.Context(), pool, migrationsDir, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrating test database: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `TRUNCATE admin_sessions, admin_users`); err != nil {
		t.Fatalf("truncating admin users: %v", err)
	}

	repo := postgres.NewAdminUserRepository(pool)
	start := make(chan struct{})
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			now := time.Now()
			errs <- repo.CreateFirst(context.Background(), &domain.AdminUser{
				ID:           fmt.Sprintf("usr_test_%d", i),
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
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_users`).Scan(&count); err != nil {
		t.Fatalf("counting admin users: %v", err)
	}
	if count != 1 {
		t.Fatalf("admin_users count = %d, want 1", count)
	}
}
