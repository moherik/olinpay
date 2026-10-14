package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/id"
)

// merchantCtxKey is the private context key holding the authenticated merchant.
type merchantCtxKey struct{}

// merchantFromContext returns the authenticated merchant for a request.
func merchantFromContext(ctx context.Context) (*domain.Merchant, bool) {
	m, ok := ctx.Value(merchantCtxKey{}).(*domain.Merchant)
	return m, ok
}

// authenticate is the API-key middleware. Credentials are accepted either via
// the X-API-Key header or as a bearer token in Authorization.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractAPIKey(r)
		if key == "" {
			fail(w, r, domain.ErrUnauthorized, "missing API key")
			return
		}

		merchant, err := s.merchants.GetByAPIKeyHash(r.Context(), id.HashAPIKey(key, s.pepper))
		if err != nil {
			fail(w, r, domain.ErrUnauthorized, "invalid API key")
			return
		}
		if !merchant.Active() {
			fail(w, r, domain.ErrForbidden, "merchant account is "+string(merchant.Status))
			return
		}

		ctx := context.WithValue(r.Context(), merchantCtxKey{}, merchant)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
