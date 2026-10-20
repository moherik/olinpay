package activity

import (
	"encoding/json"
	"net/http"
	"strings"
)

const maxBodyBytes = 64 << 10

var sensitiveHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true,
	"set-cookie": true, "x-api-key": true, "x-client-key": true,
	"x-signature": true, "x-openpay-signature": true, "x-callback-token": true,
}

var sensitiveKeys = map[string]bool{
	"authorization": true, "accesstoken": true, "apikey": true, "chargetoken": true,
	"refreshtoken": true, "idtoken": true, "identitytoken": true,
	"token": true, "clientsecret": true, "secret": true,
	"privatekey": true, "signature": true, "password": true, "cardnumber": true,
	"cardno": true, "cvv": true, "cvn": true, "pin": true, "otp": true,
	"accountnumber": true, "securitycode": true, "xsignature": true,
	"xcallbacktoken": true,
}

func RedactHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for key, values := range in {
		copyValues := append([]string(nil), values...)
		if sensitiveHeaders[strings.ToLower(key)] {
			copyValues = []string{"***"}
		}
		out[http.CanonicalHeaderKey(key)] = copyValues
	}
	return out
}

func RedactBody(body []byte) string {
	var value any
	if json.Unmarshal(body, &value) == nil {
		value = redactValue(value)
		encoded, _ := json.Marshal(value)
		return capString(string(encoded))
	}
	return capString(string(body))
}

func redactValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if sensitiveKeys[normalizeKey(key)] {
				v[key] = "***"
			} else {
				v[key] = redactValue(child)
			}
		}
	case []any:
		for i := range v {
			v[i] = redactValue(v[i])
		}
	}
	return value
}

func normalizeKey(key string) string {
	key = strings.ToLower(key)
	key = strings.NewReplacer("_", "", "-", "", " ", "").Replace(key)
	return key
}

func capString(value string) string {
	if len(value) <= maxBodyBytes {
		return value
	}
	return value[:maxBodyBytes]
}
