package id_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/moherik/openpay.id/internal/platform/id"
)

func TestNewMerchantIDShape(t *testing.T) {
	shape := regexp.MustCompile(`^AW[0-9A-Z]{8}$`)
	for i := 0; i < 100; i++ {
		got, err := id.NewMerchantID()
		if err != nil {
			t.Fatalf("NewMerchantID: %v", err)
		}
		if len(got) > id.MerchantIDLength {
			t.Fatalf("id %q longer than %d characters", got, id.MerchantIDLength)
		}
		if !shape.MatchString(got) {
			t.Fatalf("id %q does not match the AW-prefixed shape", got)
		}
	}
}

func TestNewMerchantIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		got, err := id.NewMerchantID()
		if err != nil {
			t.Fatalf("NewMerchantID: %v", err)
		}
		if seen[got] {
			t.Fatalf("duplicate id %q in 1000 draws", got)
		}
		seen[got] = true
	}
}

func TestMatchesMerchantIDShape(t *testing.T) {
	if got, err := id.NewMerchantID(); err != nil || !id.MatchesMerchantIDShape(got) {
		t.Fatalf("generated id %q (err %v) must match its own shape", got, err)
	}
	// Anything outside the generated shape does not count as one.
	for _, s := range []string{"mch_c4933bd7-14ce-4bc1-9e5d-8a026fb23245", "AW1", "awesome234", "AWO2345X9"} {
		if id.MatchesMerchantIDShape(s) {
			t.Fatalf("%q must not count as a generated merchant id", s)
		}
	}
	if !strings.HasPrefix("AW239823", id.MerchantIDPrefix) {
		t.Fatal("the documented example starts with the AW prefix")
	}
}
