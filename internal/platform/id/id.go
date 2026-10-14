// Package id centralizes identifier generation and API-key hashing so that the
// rest of the codebase never reaches for uuid or crypto/rand directly.
package id

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// New returns a new random UUID v4 string.
func New() string { return uuid.NewString() }

// NewPrefixed returns a UUID prefixed with prefix and a separator, e.g.
// NewPrefixed("trx") => "trx_1f0c...".
func NewPrefixed(prefix string) string {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		return New()
	}
	return prefix + "_" + uuid.NewString()
}

// HashAPIKey returns the hex SHA-256 of key combined with an optional pepper.
// The same function is used when storing and when verifying credentials.
func HashAPIKey(key, pepper string) string {
	sum := sha256.Sum256([]byte(pepper + key))
	return hex.EncodeToString(sum[:])
}

// GenerateAPIKey returns a new high-entropy API key of the form
// "<prefix>_<random>". The prefix is a non-secret identifier shown to the
// merchant so key rotation and lookup are easy.
func GenerateAPIKey(prefix string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("id: generating api key: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		return token, nil
	}
	return prefix + "_" + token, nil
}

// APIKeyPrefix extracts the non-secret prefix of an API key for display.
func APIKeyPrefix(key string) string {
	if i := strings.IndexByte(key, '_'); i > 0 {
		return key[:i]
	}
	if len(key) > 8 {
		return key[:8]
	}
	return key
}
