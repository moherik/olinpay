package mock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
	"github.com/moherik/openpay.id/pkg/provider/mock"
)

func newProvider(t *testing.T) *mock.Provider {
	t.Helper()
	p, err := mock.New(provider.Config{"secret": "s3cr3t", "va_prefix": "12345"})
	if err != nil {
		t.Fatalf("mock.New: %v", err)
	}
	return p
}

func create(t *testing.T, p *mock.Provider, ref string) *provider.CreatePaymentResponse {
	t.Helper()
	resp, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: ref,
		Amount:      10000,
		Method:      provider.MethodVirtualAccount,
		Channel:     "MOCK_VA",
		Expiry:      time.Hour,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	return resp
}

func TestLifecycle(t *testing.T) {
	p := newProvider(t)
	ctx := context.Background()

	resp := create(t, p, "ref-1")
	if resp.Status != provider.StatusPending {
		t.Errorf("status = %q, want pending", resp.Status)
	}
	if resp.PaymentCode == "" {
		t.Error("expected a payment code for a virtual account")
	}
	if resp.ExpiresAt == nil {
		t.Error("expected an expiry")
	}

	inquiry, err := p.Inquiry(ctx, &provider.InquiryRequest{ReferenceID: "ref-1"})
	if err != nil {
		t.Fatalf("Inquiry: %v", err)
	}
	if inquiry.Status != provider.StatusPending {
		t.Errorf("inquiry status = %q, want pending", inquiry.Status)
	}

	cancelled, err := p.Cancel(ctx, &provider.CancelRequest{ReferenceID: "ref-1", Reason: "cancel"})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != provider.StatusCancelled {
		t.Errorf("status = %q, want cancelled", cancelled.Status)
	}
}

func TestDuplicateReferenceIsRejected(t *testing.T) {
	p := newProvider(t)
	create(t, p, "ref-1")

	_, err := p.CreatePayment(context.Background(), &provider.CreatePaymentRequest{
		ReferenceID: "ref-1",
		Amount:      10000,
		Method:      provider.MethodQRIS,
	})
	if !errors.Is(err, provider.ErrDuplicateReference) {
		t.Errorf("expected ErrDuplicateReference, got %v", err)
	}
}

func TestInquiryUnknownReference(t *testing.T) {
	p := newProvider(t)
	_, err := p.Inquiry(context.Background(), &provider.InquiryRequest{ReferenceID: "nope"})
	if !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestParseWebhook(t *testing.T) {
	p := newProvider(t)
	create(t, p, "ref-1")

	body := []byte(`{"reference_id":"ref-1","status":"paid","amount":10000}`)
	evt, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if evt.Status != provider.StatusPaid {
		t.Errorf("status = %q, want paid", evt.Status)
	}
	if evt.ProviderReference == "" {
		t.Error("expected the provider reference to be enriched from local state")
	}
}

func TestParseWebhookRejectsUnknownStatus(t *testing.T) {
	p := newProvider(t)
	body := []byte(`{"reference_id":"ref-1","status":"teapot"}`)
	_, err := p.ParseWebhook(context.Background(), &provider.WebhookRequest{Body: body})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	p := newProvider(t)
	body := []byte(`{"reference_id":"ref-1","status":"paid"}`)

	valid := &provider.WebhookRequest{
		Body:    body,
		Headers: map[string][]string{mock.SignatureHeader: {mock.Sign("s3cr3t", body)}},
	}
	if err := p.VerifyWebhookSignature(valid); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}

	invalid := &provider.WebhookRequest{
		Body:    body,
		Headers: map[string][]string{mock.SignatureHeader: {"deadbeef"}},
	}
	if err := p.VerifyWebhookSignature(invalid); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got %v", err)
	}

	missing := &provider.WebhookRequest{Body: body}
	if err := p.VerifyWebhookSignature(missing); !errors.Is(err, provider.ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature for a missing header, got %v", err)
	}
}

func TestRegistryFactory(t *testing.T) {
	// The mock package registers itself under mock.Name via init().
	p, err := provider.Default().Build(mock.Name, provider.Config{})
	if err != nil {
		t.Fatalf("Build(%q): %v", mock.Name, err)
	}
	if p.Name() != mock.Name {
		t.Errorf("name = %q, want %q", p.Name(), mock.Name)
	}
}

func TestNewRegistryInheritsRegisteredFactories(t *testing.T) {
	// A freshly created registry must be able to build providers that were
	// registered globally via init(), which is how the host application wires
	// them up at startup.
	reg := provider.NewRegistry()
	if _, err := reg.Build(mock.Name, provider.Config{"secret": "x"}); err != nil {
		t.Fatalf("Build on a fresh registry: %v", err)
	}
	if reg.Len() != 1 {
		t.Errorf("registry length = %d, want 1", reg.Len())
	}
}
