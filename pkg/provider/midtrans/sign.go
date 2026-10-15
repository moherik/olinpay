package midtrans

import (
	"crypto/sha512"
	"encoding/hex"
)

// notificationSignature computes the SHA-512 hex digest Midtrans uses as a
// Core API notification's signature_key:
//
//	SHA512(order_id + status_code + gross_amount + server_key)
//
// grossAmount must be the exact string carried in the payload (e.g.
// "10000.00"); recomputing it from a parsed number would change the digest.
func notificationSignature(orderID, statusCode, grossAmount, serverKey string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + serverKey))
	return hex.EncodeToString(sum[:])
}
