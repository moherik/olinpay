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

// merchantIDAlphabet is the body alphabet of generated merchant IDs:
// digits and unambiguous uppercase letters (confusable O, I, and the digits
// 0 and 1 are excluded — merchants read and type these IDs over the phone).
const merchantIDAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// MerchantIDPrefix is the fixed namespace of merchant identifiers.
const MerchantIDPrefix = "AW"

// MerchantIDLength bounds the generated merchant ID: the 2-character prefix
// plus 8 random characters, so a fresh ID is exactly 10 characters.
const MerchantIDLength = 10

// NewMerchantID returns a short human-typed merchant identifier, e.g.
// "AW239823"-style: the AW prefix plus 8 random characters, exactly 10
// characters in total. Historical rows seeded before this format ("mch_...)
// keep working unchanged: merchant IDs are opaque strings and there is no
// length-sensitive parsing.
func NewMerchantID() (string, error) {
	buf := make([]byte, MerchantIDLength-len(MerchantIDPrefix))
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("id: generating merchant id: %w", err)
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = merchantIDAlphabet[int(b)%len(merchantIDAlphabet)]
	}
	return MerchantIDPrefix + string(out), nil
}

// MatchesMerchantIDShape reports whether s looks like a generated merchant ID.
// Pre-format rows ("mch_...") deliberately fail this and remain valid — the
// predicate exists for tests and display, not for access control.
func MatchesMerchantIDShape(s string) bool {
	if len(s) != MerchantIDLength || !strings.HasPrefix(s, MerchantIDPrefix) {
		return false
	}
	for i := len(MerchantIDPrefix); i < len(s); i++ {
		if !strings.ContainsRune(merchantIDAlphabet, rune(s[i])) {
			return false
		}
	}
	return true
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
