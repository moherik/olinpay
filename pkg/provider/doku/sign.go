package doku

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/moherik/openpay.id/pkg/provider"
)

// iso8601Layout is the timestamp layout every DOKU SNAP payload uses, e.g.
// "2023-09-19T12:11:14+07:00". DOKU requires the offset (not "Z") in both
// X-TIMESTAMP and the signature string.
const iso8601Layout = "2006-01-02T15:04:05-07:00"

// minify removes insignificant whitespace from a JSON body, producing the
// canonical form DOKU hashes. It is applied to outgoing requests (so the
// bytes we send match the bytes we signed) and to incoming callbacks (so we
// hash exactly what DOKU hashed).
func minify(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, body); err != nil {
		return nil, fmt.Errorf("doku: body is not valid JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// marshal encodes payload as compact JSON, ready to be signed and sent.
func marshal(payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("doku: encoding request: %w", err)
	}
	return minify(body)
}

// bodyHash is Lowercase(HexEncode(SHA-256(body))) as defined by the SNAP spec.
func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return strings.ToLower(hex.EncodeToString(sum[:]))
}

// requestStringToSign builds DOKU's documented symmetric signature string:
//
//	HTTPMethod + ":" + EndpointUrl + ":" + AccessToken + ":" + Lowercase(HexEncode(SHA-256(minify(RequestBody)))) + ":" + TimeStamp
func requestStringToSign(method, endpoint, accessToken string, body []byte, timestamp string) string {
	return strings.Join([]string{method, endpoint, accessToken, bodyHash(body), timestamp}, ":")
}

// signRequest produces the base64 HMAC-SHA512 signature of requestStringToSign
// that goes into the X-SIGNATURE header. The key is the merchant's
// client_secret.
func signRequest(clientSecret, method, endpoint, accessToken string, body []byte, timestamp string) (string, error) {
	mac := hmac.New(sha512.New, []byte(clientSecret))
	mac.Write([]byte(requestStringToSign(method, endpoint, accessToken, body, timestamp)))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// getTokenStringToSign builds the string signed when requesting a B2B access
// token: client_ID + "|" + X-TIMESTAMP.
func getTokenStringToSign(clientID, timestamp string) string {
	return clientID + "|" + timestamp
}

// signGetToken produces the base64 RSA-SHA256 (PKCS#1 v1.5) signature of
// getTokenStringToSign that goes into the X-SIGNATURE header of the get-token
// request.
func signGetToken(priv *rsa.PrivateKey, clientID, timestamp string) (string, error) {
	digest := sha256.Sum256([]byte(getTokenStringToSign(clientID, timestamp)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("doku: signing get-token request: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// notificationStringToSign builds the string DOKU signs on outbound
// notifications:
//
//	HTTP-METHOD:RELATIVE-URL:ACCESS-TOKEN:HEX(SHA256(requestBody)):TIMESTAMP
func notificationStringToSign(method, endpoint, accessToken string, body []byte, timestamp string) string {
	return strings.Join([]string{method, endpoint, accessToken, bodyHash(body), timestamp}, ":")
}

// verifyNotificationSignature checks a base64 HMAC-SHA512 signature produced by
// DOKU. Because DOKU's documentation is ambiguous about which access token is
// folded into the notification signature (the B2B token, the B2B2C token, or an
// empty string), we try each candidate in turn. The returned error wraps
// [provider.ErrInvalidSignature].
func verifyNotificationSignature(clientSecret, method, endpoint, accessToken string, body []byte, timestamp, signature string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("%w: signature is not valid base64: %v", provider.ErrInvalidSignature, err)
	}
	candidates := []string{""}
	if accessToken != "" {
		candidates = append(candidates, accessToken)
	}
	for _, tok := range candidates {
		mac := hmac.New(sha512.New, []byte(clientSecret))
		mac.Write([]byte(notificationStringToSign(method, endpoint, tok, body, timestamp)))
		if hmac.Equal(mac.Sum(nil), sig) {
			return nil
		}
	}
	return fmt.Errorf("%w: HMAC-SHA512 mismatch", provider.ErrInvalidSignature)
}

// parsePrivateKey accepts either a PEM block (PKCS#1 "RSA PRIVATE KEY" or
// PKCS#8 "PRIVATE KEY") or the bare base64-encoded DER that the DOKU docs use.
func parsePrivateKey(raw string) (*rsa.PrivateKey, error) {
	der, err := keyDER(raw, "private key")
	if err != nil {
		return nil, err
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("doku: private key is neither PKCS#1 nor PKCS#8 RSA")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("doku: private key is not an RSA key")
	}
	return key, nil
}

// keyDER normalizes the several shapes a key can take when it comes from an
// environment variable into raw DER bytes.
func keyDER(raw, what string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("doku: empty %s", what)
	}
	if block, _ := pem.Decode([]byte(raw)); block != nil {
		return block.Bytes, nil
	}
	// Single-line (or newline-wrapped) base64 DER: strip any line breaks that
	// survived environment/JSON encoding.
	compact := strings.NewReplacer("\n", "", "\r", "", " ", "", "\t", "").Replace(raw)
	der, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("doku: %s is neither PEM nor base64 DER: %w", what, err)
	}
	return der, nil
}
