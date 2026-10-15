package midtranssnap

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

// iso8601Layout is the timestamp layout Midtrans SNAP payloads use, e.g.
// "2024-03-19T14:30:00+07:00". Midtrans requires the numeric offset (not "Z")
// in both X-TIMESTAMP and the signature string.
const iso8601Layout = "2006-01-02T15:04:05-07:00"

// minify removes insignificant whitespace from a JSON body, producing the
// canonical form Midtrans hashes. It is applied to outgoing requests (so the
// bytes we send match the bytes we signed) and to incoming callbacks (so we
// hash exactly what Midtrans hashed).
func minify(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, body); err != nil {
		return nil, fmt.Errorf("midtrans-snap: body is not valid JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// marshal encodes payload as compact JSON, ready to be signed and sent.
func marshal(payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("midtrans-snap: encoding request: %w", err)
	}
	return minify(body)
}

// bodyHash is Lowercase(HexEncode(SHA-256(body))) as defined by the SNAP spec.
func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return strings.ToLower(hex.EncodeToString(sum[:]))
}

// requestStringToSign builds Midtrans' documented symmetric signature string:
//
//	HTTPMethod + ":" + EndpointUrl + ":" + AccessToken + ":" + Lowercase(HexEncode(SHA-256(minify(RequestBody)))) + ":" + TimeStamp
//
// EndpointUrl is the path only, e.g. "/v1.0/debit/payment-host-to-host".
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

// tokenStringToSign builds the string signed when requesting a B2B access
// token: ClientID + "|" + X-TIMESTAMP.
func tokenStringToSign(clientID, timestamp string) string {
	return clientID + "|" + timestamp
}

// signToken produces the base64 RSA-SHA256 (PKCS#1 v1.5) signature of
// tokenStringToSign that goes into the X-SIGNATURE header of the get-token
// request.
func signToken(priv *rsa.PrivateKey, clientID, timestamp string) (string, error) {
	digest := sha256.Sum256([]byte(tokenStringToSign(clientID, timestamp)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("midtrans-snap: signing access-token request: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// notificationStringToSign builds the string Midtrans signs on outbound
// notifications:
//
//	HTTPMethod + ":" + EndpointUrl + ":" + Lowercase(HexEncode(SHA-256(minify(RequestBody)))) + ":" + TimeStamp
//
// Unlike the transactional signature it carries no access token: Midtrans signs
// with its own private key and the merchant verifies with Midtrans' public key.
func notificationStringToSign(method, endpoint string, body []byte, timestamp string) string {
	return strings.Join([]string{method, endpoint, bodyHash(body), timestamp}, ":")
}

// verifyNotificationSignature checks a base64 RSA-SHA256 signature produced by
// Midtrans using Midtrans' public key. The string that was signed contains the
// SHA-256 of the minified request body, so the body is minified here as well.
// The returned error wraps [provider.ErrInvalidSignature].
func verifyNotificationSignature(pub *rsa.PublicKey, method, endpoint string, body []byte, timestamp, signature string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("%w: signature is not valid base64: %v", provider.ErrInvalidSignature, err)
	}
	canonical, err := minify(body)
	if err != nil {
		return fmt.Errorf("%w: %v", provider.ErrInvalidSignature, err)
	}
	digest := sha256.Sum256([]byte(notificationStringToSign(method, endpoint, canonical, timestamp)))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return fmt.Errorf("%w: %v", provider.ErrInvalidSignature, err)
	}
	return nil
}

// parsePrivateKey accepts either a PEM block (PKCS#1 "RSA PRIVATE KEY" or
// PKCS#8 "PRIVATE KEY") or the bare base64-encoded DER that the Midtrans docs
// use.
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
		return nil, errors.New("midtrans-snap: private key is neither PKCS#1 nor PKCS#8 RSA")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("midtrans-snap: private key is not an RSA key")
	}
	return key, nil
}

// parsePublicKey accepts a PEM block or bare base64 DER, in PKIX
// ("PUBLIC KEY") or PKCS#1 ("RSA PUBLIC KEY") form.
func parsePublicKey(raw string) (*rsa.PublicKey, error) {
	der, err := keyDER(raw, "public key")
	if err != nil {
		return nil, err
	}
	if parsed, err := x509.ParsePKIXPublicKey(der); err == nil {
		key, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("midtrans-snap: public key is not an RSA key")
		}
		return key, nil
	}
	if key, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return key, nil
	}
	return nil, errors.New("midtrans-snap: public key is neither PKIX nor PKCS#1")
}

// keyDER normalizes the several shapes a key can take when it comes from an
// environment variable into raw DER bytes.
func keyDER(raw, what string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("midtrans-snap: empty %s", what)
	}
	if block, _ := pem.Decode([]byte(raw)); block != nil {
		return block.Bytes, nil
	}
	// Single-line (or newline-wrapped) base64 DER: strip any line breaks that
	// survived environment/JSON encoding.
	compact := strings.NewReplacer("\n", "", "\r", "", " ", "", "\t", "").Replace(raw)
	der, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("midtrans-snap: %s is neither PEM nor base64 DER: %w", what, err)
	}
	return der, nil
}
