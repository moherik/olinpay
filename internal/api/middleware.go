package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/id"
)

// merchantCtxKey is the private context key holding the authenticated merchant.
type merchantCtxKey struct{}

// environmentCtxKey is the private context key holding the environment the
// authenticated API key belongs to (sandbox or production).
type environmentCtxKey struct{}

// apiKeyCtxKey is the private context key holding the resolved API key record
// when the credential lives in the api_keys table (nil for legacy
// merchants.api_key_hash credentials).
type apiKeyCtxKey struct{}

// merchantFromContext returns the authenticated merchant for a request.
func merchantFromContext(ctx context.Context) (*domain.Merchant, bool) {
	m, ok := ctx.Value(merchantCtxKey{}).(*domain.Merchant)
	return m, ok
}

// environmentFromContext returns the authenticated environment for a request,
// defaulting to sandbox.
func environmentFromContext(ctx context.Context) string {
	if env, ok := ctx.Value(environmentCtxKey{}).(string); ok && env != "" {
		return env
	}
	return domain.EnvironmentSandbox
}

// apiKeyFromContext returns the resolved api_keys record backing the
// credential, when the credential was issued as a discrete key.
func apiKeyFromContext(ctx context.Context) (*domain.APIKey, bool) {
	k, ok := ctx.Value(apiKeyCtxKey{}).(*domain.APIKey)
	return k, ok
}

// authenticate is the API-key middleware. Credentials are accepted either via
// the X-API-Key header or as a bearer token in Authorization.
//
// Two credential paths exist:
//
//   - api_keys rows (admin-issued, labeled, per environment). The key decides
//     the environment of everything created under it.
//   - the legacy merchants.api_key_hash column. Its environment is inferred
//     from the key prefix so pre-admin integrations keep working.
//
// Revoked or expired keys must fail exactly like unknown keys, without
// revealing which one it was.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractAPIKey(r)
		if key == "" {
			fail(w, r, domain.ErrUnauthorized, "missing API key")
			return
		}

		hash := id.HashAPIKey(key, s.pepper)

		var (
			merchant    *domain.Merchant
			resolvedKey *domain.APIKey
		)
		if stored, err := s.apiKeys.GetByHash(r.Context(), hash); err == nil && stored != nil {
			if stored.Revoked() {
				fail(w, r, domain.ErrUnauthorized, "invalid API key")
				return
			}
			merchant, err = s.merchants.GetByID(r.Context(), stored.MerchantID)
			if err != nil {
				fail(w, r, domain.ErrUnauthorized, "invalid API key")
				return
			}
			resolvedKey = stored
		} else if legacy, err := s.merchants.GetByAPIKeyHash(r.Context(), hash); err == nil && legacy != nil {
			merchant = legacy
		} else {
			fail(w, r, domain.ErrUnauthorized, "invalid API key")
			return
		}

		if !merchant.Active() {
			fail(w, r, domain.ErrForbidden, "merchant account is "+string(merchant.Status))
			return
		}

		env := domain.EnvironmentSandbox
		if resolvedKey != nil {
			env = resolvedKey.Environment
		} else if strings.Contains(key, "_live_") || strings.HasPrefix(key, "sk_live") {
			env = domain.EnvironmentProduction
		}

		if resolvedKey != nil {
			// Best effort: last-used tracking must never fail a payment.
			if resolvedKey.LastUsedAt == nil || time.Since(*resolvedKey.LastUsedAt) > time.Minute {
				resolvedKey.LastUsedAt = nowPtr()
				_ = s.apiKeys.Update(r.Context(), resolvedKey)
			}
		}

		ctx := context.WithValue(r.Context(), merchantCtxKey{}, merchant)
		ctx = context.WithValue(ctx, environmentCtxKey{}, env)
		if resolvedKey != nil {
			ctx = context.WithValue(ctx, apiKeyCtxKey{}, resolvedKey)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// nowPtr is swappable time for tests.
var nowPtr = func() *time.Time {
	t := time.Now()
	return &t
}

func extractAPIKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key != "" {
		return key
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return ""
	}
	if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return auth
}
