package winpay

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
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

// iso8601Layout is the timestamp layout every Winpay SNAP payload uses, e.g.
// "2023-09-19T12:11:14+07:00".
const iso8601Layout = "2006-01-02T15:04:05-07:00"

// minify removes insignificant whitespace from a JSON body, producing the
// canonical form Winpay hashes. It is applied to outgoing requests (so the
// bytes we send match the bytes we signed) and to incoming callbacks (so we
// hash exactly what Winpay hashed).
func minify(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, body); err != nil {
		return nil, fmt.Errorf("winpay: body is not valid JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// marshal encodes payload as compact JSON, ready to be signed and sent.
func marshal(payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("winpay: encoding request: %w", err)
	}
	return minify(body)
}

// bodyHash is Lowercase(HexEncode(SHA-256(body))) as defined by the SNAP spec.
func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return strings.ToLower(hex.EncodeToString(sum[:]))
}

// stringToSign builds Winpay's documented signature string:
//
//	HTTPMethod + ":" + EndpointUrl + ":" + Lowercase(HexEncode(SHA-256(minify(RequestBody)))) + ":" + Timestamp
func stringToSign(method, endpoint string, body []byte, timestamp string) string {
	return strings.Join([]string{method, endpoint, bodyHash(body), timestamp}, ":")
}

// signRequest produces the base64 RSA-SHA256 (PKCS#1 v1.5) signature of
// stringToSign that goes into the X-SIGNATURE header.
func signRequest(priv *rsa.PrivateKey, method, endpoint string, body []byte, timestamp string) (string, error) {
	digest := sha256.Sum256([]byte(stringToSign(method, endpoint, body, timestamp)))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("winpay: signing request: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// verifySignature checks a base64 RSA-SHA256 signature produced by Winpay. The
// returned error wraps [provider.ErrInvalidSignature].
func verifySignature(pub *rsa.PublicKey, method, endpoint string, body []byte, timestamp, signature string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("%w: signature is not valid base64: %v", provider.ErrInvalidSignature, err)
	}
	digest := sha256.Sum256([]byte(stringToSign(method, endpoint, body, timestamp)))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return fmt.Errorf("%w: %v", provider.ErrInvalidSignature, err)
	}
	return nil
}

// parsePrivateKey accepts either a PEM block (PKCS#1 "RSA PRIVATE KEY" or
// PKCS#8 "PRIVATE KEY") or the bare base64-encoded DER that the Winpay docs use.
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
		return nil, errors.New("winpay: private key is neither PKCS#1 nor PKCS#8 RSA")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("winpay: private key is not an RSA key")
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
			return nil, errors.New("winpay: public key is not an RSA key")
		}
		return key, nil
	}
	if key, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return key, nil
	}
	return nil, errors.New("winpay: public key is neither PKIX nor PKCS#1")
}

// keyDER normalizes the several shapes a key can take when it comes from an
// environment variable into raw DER bytes.
func keyDER(raw, what string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("winpay: empty %s", what)
	}
	if block, _ := pem.Decode([]byte(raw)); block != nil {
		return block.Bytes, nil
	}
	// Single-line (or newline-wrapped) base64 DER: strip any line breaks that
	// survived environment/JSON encoding.
	compact := strings.NewReplacer("\n", "", "\r", "", " ", "", "\t", "").Replace(raw)
	der, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return nil, fmt.Errorf("winpay: %s is neither PEM nor base64 DER: %w", what, err)
	}
	return der, nil
}
