package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/pkg/provider"
)

// stubProvider is a minimal provider used to exercise routing logic.
type stubProvider struct {
	name string
	caps provider.Capabilities
}

func (s *stubProvider) Name() string                        { return s.name }
func (s *stubProvider) Capabilities() provider.Capabilities { return s.caps }
func (s *stubProvider) CreatePayment(context.Context, *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
	return &provider.CreatePaymentResponse{Status: provider.StatusPending}, nil
}
func (s *stubProvider) Inquiry(context.Context, *provider.InquiryRequest) (*provider.InquiryResponse, error) {
	return &provider.InquiryResponse{Status: provider.StatusPending}, nil
}
func (s *stubProvider) Cancel(context.Context, *provider.CancelRequest) (*provider.CancelResponse, error) {
	return &provider.CancelResponse{Status: provider.StatusCancelled}, nil
}
func (s *stubProvider) ParseWebhook(context.Context, *provider.WebhookRequest) (*provider.WebhookEvent, error) {
	return &provider.WebhookEvent{Status: provider.StatusPaid}, nil
}

func candidates(names ...string) []core.Candidate {
	out := make([]core.Candidate, len(names))
	for i, n := range names {
		out[i] = core.Candidate{Provider: &stubProvider{name: n}, Position: i}
	}
	return out
}

func names(cs []core.Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Provider.Name()
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPriorityStrategyOrdersByPriority(t *testing.T) {
	cs := candidates("a", "b", "c")
	cs[0].Priority = 5
	cs[1].Priority = 1
	cs[2].Priority = 3

	got := names((&core.PriorityStrategy{}).Order(cs, &core.RouteRequest{}))
	want := []string{"b", "c", "a"}
	if !equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestRoundRobinStrategyRotates(t *testing.T) {
	cs := candidates("a", "b", "c")
	s := core.NewRoundRobinStrategy()
	req := &core.RouteRequest{MerchantID: "m1", Method: provider.MethodQRIS}

	want := [][]string{
		{"a", "b", "c"},
		{"b", "c", "a"},
		{"c", "a", "b"},
		{"a", "b", "c"},
	}
	for i, w := range want {
		if got := names(s.Order(cs, req)); !equal(got, w) {
			t.Errorf("call %d: order = %v, want %v", i, got, w)
		}
	}
}

func TestRouterFiltersByCapability(t *testing.T) {
	qrOnly := &stubProvider{name: "qr", caps: provider.Capabilities{
		Methods: []provider.Method{provider.MethodQRIS}, SupportsInquiry: true,
	}}
	vaOnly := &stubProvider{name: "va", caps: provider.Capabilities{
		Methods: []provider.Method{provider.MethodVirtualAccount}, SupportsInquiry: true,
	}}
	registry := provider.NewRegistry()
	registry.Add(qrOnly)
	registry.Add(vaOnly)

	router := core.NewRouter(registry)
	merchant := &domain.Merchant{
		ID: "m1",
		Routing: domain.RoutingConfig{
			Strategy: domain.StrategyPriority,
			Providers: []domain.RoutingProvider{
				{Name: "qr", Enabled: true},
				{Name: "va", Enabled: true},
			},
		},
	}

	got, err := router.Order(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodQRIS})
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if len(got) != 1 || got[0].Provider.Name() != "qr" {
		t.Errorf("qr request routed to %v, want [qr]", names(got))
	}

	got, err = router.Order(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodVirtualAccount})
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if len(got) != 1 || got[0].Provider.Name() != "va" {
		t.Errorf("va request routed to %v, want [va]", names(got))
	}
}

func TestRouterFallsBackToAllProvidersWithoutConfig(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Add(&stubProvider{name: "a", caps: provider.Capabilities{SupportsInquiry: true}})
	registry.Add(&stubProvider{name: "b", caps: provider.Capabilities{SupportsInquiry: true}})

	router := core.NewRouter(registry)
	merchant := &domain.Merchant{ID: "m1"}

	got, err := router.Order(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodQRIS})
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected both providers as candidates, got %v", names(got))
	}
}

func TestRouterReturnsNoRouteWhenNothingMatches(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Add(&stubProvider{name: "qr", caps: provider.Capabilities{
		Methods: []provider.Method{provider.MethodQRIS},
	}})

	router := core.NewRouter(registry)
	merchant := &domain.Merchant{
		ID: "m1",
		Routing: domain.RoutingConfig{
			Providers: []domain.RoutingProvider{{Name: "qr", Enabled: true}},
		},
	}

	_, err := router.Order(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodVirtualAccount})
	if err == nil {
		t.Fatal("expected an error for an unsupported method")
	}
	if !errors.Is(err, domain.ErrNoRoute) {
		t.Errorf("error = %v, want ErrNoRoute", err)
	}
}
