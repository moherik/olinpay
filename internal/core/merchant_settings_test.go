package core_test

import (
	"context"
	"testing"

	"github.com/moherik/openpay.id/internal/domain"
)

func TestCreatePaymentUsesMerchantReturnURLSettings(t *testing.T) {
	h := newHarness(t)
	capture := &returnURLCaptureProvider{Provider: h.gateway}
	h.registry.Remove(capture.Name())
	h.registry.Add(capture)
	if err := h.store.MerchantSettings().Upsert(context.Background(), &domain.MerchantSettings{
		MerchantID: h.merchant.ID, ReturnURL: "https://shop.example/return",
		SuccessReturnURL: "https://shop.example/paid", FailureReturnURL: "https://shop.example/failed",
	}); err != nil {
		t.Fatalf("saving merchant settings: %v", err)
	}
	if _, err := h.engine.CreatePayment(context.Background(), h.createCommand()); err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if capture.returnURL != "https://shop.example/return" || capture.successReturnURL != "https://shop.example/paid" || capture.failureReturnURL != "https://shop.example/failed" {
		t.Fatalf("provider return URLs = (%q, %q, %q)", capture.returnURL, capture.successReturnURL, capture.failureReturnURL)
	}
}
