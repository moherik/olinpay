package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/moherik/openpay.id/internal/config"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/logger"
	"github.com/moherik/openpay.id/internal/testutil/memory"
	"github.com/moherik/openpay.id/pkg/provider"
	_ "github.com/moherik/openpay.id/pkg/provider/mock" // registers the mock factory
)

func testLogger() *slog.Logger { return logger.New("error", io.Discard) }

// A deployment may rely entirely on console-saved credentials: with no
// OPENPAY_PROVIDERS at all, the DB rows must build the registry. Regresses the
// old behavior where `serve` aborted on an empty registry before looking at
// the database.
func TestBuildRegistryFromDatabaseOnly(t *testing.T) {
	store := memory.NewStore()
	err := store.ProviderConfigs().Upsert(t.Context(), &domain.ProviderConfig{
		Provider:    "mock",
		Environment: "sandbox",
		Config:      map[string]string{},
	})
	if err != nil {
		t.Fatalf("storing provider config: %v", err)
	}

	cfg := &config.Config{ProviderConfigs: map[string]provider.Config{}}
	registry, err := buildRegistry(cfg, store.ProviderConfigs(), testLogger())
	if err != nil {
		t.Fatalf("buildRegistry: %v", err)
	}

	if registry.Len() != 1 {
		t.Fatalf("expected 1 DB-built instance, got %d (%v)", registry.Len(), registry.Names())
	}
	if got := registry.Names(); !strings.Contains(strings.Join(got, ","), domain.InstanceName("mock", "sandbox")) {
		t.Errorf("expected a %q instance, got %v", domain.InstanceName("mock", "sandbox"), got)
	}
}

// Empty environment and empty database is the valid first-install state: the
// admin setup flow remains available to configure the first provider.
func TestBuildRegistryEmpty(t *testing.T) {
	cfg := &config.Config{ProviderConfigs: map[string]provider.Config{}}
	store := memory.NewStore()

	registry, err := buildRegistry(cfg, store.ProviderConfigs(), testLogger())
	if err != nil {
		t.Fatalf("buildRegistry: %v", err)
	}
	if registry == nil {
		t.Fatal("buildRegistry returned a nil registry")
	}
	if registry.Len() != 0 {
		t.Errorf("expected no provider instances, got %d (%v)", registry.Len(), registry.Names())
	}
}

// Listing an adapter in OPENPAY_PROVIDERS without its required environment
// config still fails at boot: env-built instances are validated eagerly, and
// DB rows for other adapters do not paper over that.
func TestBuildRegistryEnvBuildFailure(t *testing.T) {
	cfg := &config.Config{
		Providers:       []string{"midtrans"},
		ProviderConfigs: map[string]provider.Config{"midtrans": {}},
	}
	store := memory.NewStore()

	_, err := buildRegistry(cfg, store.ProviderConfigs(), testLogger())
	if err == nil {
		t.Fatal("expected the missing midtrans server_key to fail the build")
	}
	if !strings.Contains(err.Error(), `building provider "midtrans"`) {
		t.Errorf("error should name the failing provider, got %q", err.Error())
	}
}
