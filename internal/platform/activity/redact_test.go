package activity_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/moherik/openpay.id/internal/platform/activity"
)

func TestRedactHeadersRemovesCredentialValues(t *testing.T) {
	got := activity.RedactHeaders(http.Header{
		"Authorization":  {"Bearer secret"},
		"X-OpenPay-Signature": {"signature"},
		"Content-Type":    {"application/json"},
	})
	if got.Get("Authorization") != "***" || got.Get("X-OpenPay-Signature") != "***" {
		t.Fatalf("sensitive headers were not redacted: %#v", got)
	}
	if got.Get("Content-Type") != "application/json" {
		t.Fatalf("safe header changed: %#v", got)
	}
}

func TestRedactBodyRedactsNestedSecretsAndCapsLength(t *testing.T) {
	got := activity.RedactBody([]byte(`{"token":"secret","nested":{"password":"hidden"},"ok":"yes"}`))
	if strings.Contains(got, "secret") || strings.Contains(got, "hidden") {
		t.Fatalf("body leaked sensitive values: %s", got)
	}
	if !strings.Contains(got, `"ok":"yes"`) {
		t.Fatalf("safe body field missing: %s", got)
	}
	if len(activity.RedactBody([]byte(strings.Repeat("x", 70<<10)))) > 64<<10 {
		t.Fatal("redacted body exceeded 64 KiB")
	}
}
