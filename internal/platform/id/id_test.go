package id_test

import (
	"testing"

	"github.com/moherik/openpay.id/internal/platform/id"
)

func TestNewMerchantIDShape(t *testing.T) {
	for i := 0; i < 100; i++ {
		got, err := id.NewMerchantID()
		if err != nil {
			t.Fatalf("NewMerchantID: %v", err)
		}
		if !id.ValidMerchantID(got) {
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

func TestValidMerchantID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "valid", id: "AW9X2K4T7Z", want: true},
		{name: "invalid prefix", id: "BW9X2K4T7Z"},
		{name: "lowercase", id: "AW9x2K4T7Z"},
		{name: "forbidden zero", id: "AW0X2K4T7Z"},
		{name: "forbidden one", id: "AW1X2K4T7Z"},
		{name: "forbidden I", id: "AWIX2K4T7Z"},
		{name: "forbidden O", id: "AWOX2K4T7Z"},
		{name: "nine characters", id: "AW9X2K4T"},
		{name: "eleven characters", id: "AW9X2K4T7ZQ"},
		{name: "historical ID", id: "mch_c4933bd7-14ce-4bc1-9e5d-8a026fb23245"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := id.ValidMerchantID(tt.id); got != tt.want {
				t.Errorf("ValidMerchantID(%q) = %t, want %t", tt.id, got, tt.want)
			}
		})
	}
}
