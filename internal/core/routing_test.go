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

	dec, err := router.Decide(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodQRIS})
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	got := dec.Candidates
	if len(got) != 1 || got[0].Provider.Name() != "qr" {
		t.Errorf("qr request routed to %v, want [qr]", names(got))
	}

	dec, err = router.Decide(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodVirtualAccount})
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	got := dec.Candidates
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

	dec, err := router.Decide(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodQRIS})
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	got := dec.Candidates
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

	_, err := router.Decide(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodVirtualAccount})
	if err == nil {
		t.Fatal("expected an error for an unsupported method")
	}
	if !errors.Is(err, domain.ErrNoRoute) {
		t.Errorf("error = %v, want ErrNoRoute", err)
	}
}

func TestRouterScopesCandidatesByEnvironment(t *testing.T) {
	sandboxOnly := &stubProvider{name: "winpay.sandbox", caps: provider.Capabilities{SupportsInquiry: true}}
	prodOnly := &stubProvider{name: "winpay.production", caps: provider.Capabilities{SupportsInquiry: true}}
	legacy := &stubProvider{name: "mock", caps: provider.Capabilities{SupportsInquiry: true}}
	registry := provider.NewRegistry()
	registry.Add(sandboxOnly)
	registry.Add(prodOnly)
	registry.Add(legacy)

	router := core.NewRouter(registry)

	// No config: each environment only sees its own instances plus legacy ones.
	merchant := &domain.Merchant{ID: "m1"}

	dec, err := router.Decide(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodQRIS, Environment: domain.EnvironmentSandbox})
	if err != nil {
		t.Fatalf("Order sandbox: %v", err)
	}
	got := dec.Candidates
	if len(got) != 2 || (got[0].Provider != sandboxOnly && got[1].Provider != sandboxOnly) || (got[0].Provider != legacy && got[1].Provider != legacy) {
		t.Errorf("sandbox candidates = %v, want [winpay.sandbox mock]", names(got))
	}

	dec, err = router.Decide(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodQRIS, Environment: domain.EnvironmentProduction})
	if err != nil {
		t.Fatalf("Order production: %v", err)
	}
	got = dec.Candidates
	if len(got) != 2 || (got[0].Provider != prodOnly && got[1].Provider != prodOnly) || (got[0].Provider != legacy && got[1].Provider != legacy) {
		t.Errorf("production candidates = %v, want [winpay.production mock]", names(got))
	}

	// Explicit config: sandbox traffic skips production-only entries.
	merchant = &domain.Merchant{ID: "m1", Routing: domain.RoutingConfig{
		Providers: []domain.RoutingProvider{
			{Name: "winpay.production", Enabled: true},
			{Name: "winpay.sandbox", Enabled: true},
		},
	}}
	dec, err = router.Decide(merchant, &core.RouteRequest{MerchantID: "m1", Method: provider.MethodQRIS, Environment: domain.EnvironmentSandbox})
	if err != nil {
		t.Fatalf("Order sandbox config: %v", err)
	}
	got = dec.Candidates
	if len(got) != 1 || got[0].Provider != sandboxOnly {
		t.Errorf("sandbox routed to %v, want [winpay.sandbox]", names(got))
	}
}

func TestRuleChainPrependsMatchingRule(t *testing.T) {
	primary := &stubProvider{name: "winpay.sandbox", caps: provider.Capabilities{SupportsInquiry: true}}
	fallback := &stubProvider{name: "mock", caps: provider.Capabilities{SupportsInquiry: true}}
	registry := provider.NewRegistry()
	registry.Add(primary)
	registry.Add(fallback)

	router := core.NewRouter(registry)
	merchant := &domain.Merchant{ID: "m1", Routing: domain.RoutingConfig{
		Strategy: domain.StrategyPriority,
		Providers: []domain.RoutingProvider{
			{Name: "mock", Enabled: true},
		},
		Rules: []domain.RoutingRule{
			{
				Condition: domain.RoutingCondition{
					Field:    domain.ConditionFieldAmount,
					Operator: domain.ConditionOperatorGte,
					Value:    "10000",
				},
				Providers: []string{"winpay.sandbox"},
			},
		},
	}}

	dec, err := router.Decide(merchant, &core.RouteRequest{
		MerchantID: "m1", Environment: domain.EnvironmentSandbox,
		Method: provider.MethodQRIS, Amount: 15000,
	})
	if err != nil {
		t.Fatalf("Order rule match: %v", err)
	}
	got := dec.Candidates
	if len(got) != 2 || got[0].Provider != primary || got[1].Provider != fallback {
		t.Errorf("rule-match candidates = %v, want [winpay.sandbox mock]", names(got))
	}

	// Below the threshold the rule does not fire.
	dec, err = router.Decide(merchant, &core.RouteRequest{
		MerchantID: "m1", Environment: domain.EnvironmentSandbox,
		Method: provider.MethodQRIS, Amount: 9999,
	})
	if err != nil {
		t.Fatalf("Order no match: %v", err)
	}
	got = dec.Candidates
	if len(got) != 1 || got[0].Provider != fallback {
		t.Errorf("no-match candidates = %+v, want base chain", got)
	}

	// A metadata condition only fires when the request carries the key.
	merchant.Routing.Rules = []domain.RoutingRule{
		{
			Condition: domain.RoutingCondition{
				Field: "metadata:acquirer", Operator: domain.ConditionOperatorEq, Value: "xendit",
			},
			Providers: []string{"winpay.sandbox"},
		},
	}
	dec, err = router.Decide(merchant, &core.RouteRequest{
		MerchantID: "m1", Environment: domain.EnvironmentSandbox,
		Method: provider.MethodQRIS, Amount: 15000, Metadata: map[string]string{"acquirer": "xendit"},
	})
	if err != nil {
		t.Fatalf("Order metadata match: %v", err)
	}
	got = dec.Candidates
	if got[0].Provider != primary {
		t.Errorf("metadata candidates = %+v, want rule first", got)
	}
	dec, err = router.Decide(merchant, &core.RouteRequest{
		MerchantID: "m1", Environment: domain.EnvironmentSandbox,
		Method: provider.MethodQRIS, Amount: 15000, Metadata: map[string]string{"acquirer": "other"},
	})
	if err != nil {
		t.Fatalf("Order metadata mismatch: %v", err)
	}
	got = dec.Candidates
	if got[0].Provider != fallback {
		t.Errorf("mismatch candidates = %+v, want base chain", got)
	}
	dec, err = router.Decide(merchant, &core.RouteRequest{
		MerchantID: "m1", Environment: domain.EnvironmentSandbox,
		Method: provider.MethodQRIS, Amount: 15000,
	})
	if err != nil {
		t.Fatalf("Order metadata absent: %v", err)
	}
	got = dec.Candidates
	if got[0].Provider != fallback {
		t.Errorf("absent candidates = %+v, want base chain", got)
	}

	// Channel conditions compare the raw channel string.
	merchant.Routing.Rules = []domain.RoutingRule{
		{
			Condition: domain.RoutingCondition{
				Field: domain.ConditionFieldChannel, Operator: domain.ConditionOperatorEq, Value: "SHOPEE",
			},
			Providers: []string{"winpay.sandbox"},
		},
	}
	dec, err = router.Decide(merchant, &core.RouteRequest{
		MerchantID: "m1", Environment: domain.EnvironmentSandbox,
		Method: provider.MethodVirtualAccount, Channel: "SHOPEE", Amount: 15000,
	})
	if err != nil {
		t.Fatalf("Order channel match: %v", err)
	}
	got = dec.Candidates
	if got[0].Provider != primary {
		t.Errorf("channel candidates = %+v, want rule first", got)
	}
}

func TestOrderForProviderResolvesEnvironmentInstance(t *testing.T) {
	sandboxOnly := &stubProvider{name: "winpay.sandbox", caps: provider.Capabilities{SupportsInquiry: true}}
	registry := provider.NewRegistry()
	registry.Add(sandboxOnly)
	router := core.NewRouter(registry)

	// A bare adapter name resolves to the environment-scoped instance.
	got, err := router.OrderForProvider("winpay", &core.RouteRequest{
		Method: provider.MethodQRIS, Environment: domain.EnvironmentSandbox,
	})
	if err != nil {
		t.Fatalf("OrderForProvider: %v", err)
	}
	if got[0].Provider != sandboxOnly {
		t.Errorf("resolved %v, want winpay.sandbox", got[0].Provider.Name())
	}

	// The exact instance name keeps working.
	if _, err = router.OrderForProvider("winpay.sandbox", &core.RouteRequest{
		Method: provider.MethodQRIS, Environment: domain.EnvironmentSandbox,
	}); err != nil {
		t.Fatalf("OrderForProvider exact: %v", err)
	}

	// Cross-environment forcing is rejected.
	if _, err = router.OrderForProvider("winpay.sandbox", &core.RouteRequest{
		Method: provider.MethodQRIS, Environment: domain.EnvironmentProduction,
	}); !errors.Is(err, domain.ErrNoRoute) {
		t.Errorf("error = %v, want ErrNoRoute", err)
	}

	// Unknown adapters are still a no-route error.
	if _, err = router.OrderForProvider("nope", &core.RouteRequest{
		Method: provider.MethodQRIS, Environment: domain.EnvironmentSandbox,
	}); !errors.Is(err, domain.ErrNoRoute) {
		t.Errorf("error = %v, want ErrNoRoute", err)
	}
}
