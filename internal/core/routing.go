package core

import (
	"fmt"
	"sort"
	"sync"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/pkg/provider"
)

// RouteRequest is the subset of a payment request that routing cares about.
type RouteRequest struct {
	// MerchantID scopes stateful strategies (e.g. round-robin counters).
	MerchantID string
	// Method is the unified payment method being requested.
	Method provider.Method
	// Channel optionally narrows the instrument.
	Channel string
	// Amount and Currency are available to amount-based strategies.
	Amount   provider.Amount
	Currency provider.Currency
}

// Candidate is a provider eligible to serve a request, together with the
// routing metadata that came from the merchant's configuration.
type Candidate struct {
	Provider provider.Provider
	Priority int
	Weight   int
	Position int
}

// Strategy decides in which order eligible providers should be tried. The
// engine treats the head of the slice as the primary provider and the remainder
// as an ordered failover chain, so a strategy only needs to sort/rotate.
type Strategy interface {
	// Name is the strategy identifier used in merchant configuration.
	Name() string
	// Order returns a new slice; it must not mutate the input.
	Order(candidates []Candidate, req *RouteRequest) []Candidate
}

// PriorityStrategy orders candidates by ascending Priority, preserving the
// configured order for equal priorities (sort is stable).
type PriorityStrategy struct{}

// Name implements Strategy.
func (*PriorityStrategy) Name() string { return domain.StrategyPriority }

// Order implements Strategy.
func (*PriorityStrategy) Order(candidates []Candidate, _ *RouteRequest) []Candidate {
	out := append([]Candidate(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// RoundRobinStrategy rotates the starting provider per merchant (and method) so
// that load is spread evenly. It is safe for concurrent use.
type RoundRobinStrategy struct {
	mu       sync.Mutex
	counters map[string]uint64
}

// NewRoundRobinStrategy returns an empty round-robin strategy.
func NewRoundRobinStrategy() *RoundRobinStrategy {
	return &RoundRobinStrategy{counters: make(map[string]uint64)}
}

// Name implements Strategy.
func (*RoundRobinStrategy) Name() string { return domain.StrategyRoundRobin }

// Order implements Strategy.
func (s *RoundRobinStrategy) Order(candidates []Candidate, req *RouteRequest) []Candidate {
	if len(candidates) <= 1 {
		return append([]Candidate(nil), candidates...)
	}
	key := req.MerchantID + "|" + string(req.Method)

	s.mu.Lock()
	start := int(s.counters[key] % uint64(len(candidates)))
	s.counters[key]++
	s.mu.Unlock()

	out := make([]Candidate, 0, len(candidates))
	out = append(out, candidates[start:]...)
	out = append(out, candidates[:start]...)
	return out
}

// Router turns a merchant's routing configuration into an ordered list of
// eligible providers.
type Router struct {
	registry   *provider.Registry
	strategies map[string]Strategy
	fallback   Strategy
}

// NewRouter builds a router with the built-in strategies registered.
func NewRouter(registry *provider.Registry) *Router {
	r := &Router{
		registry:   registry,
		strategies: make(map[string]Strategy),
	}
	r.RegisterStrategy(&PriorityStrategy{})
	r.RegisterStrategy(NewRoundRobinStrategy())
	r.fallback = r.strategies[domain.StrategyPriority]
	return r
}

// RegisterStrategy adds or replaces a strategy by name.
func (r *Router) RegisterStrategy(s Strategy) {
	if s == nil {
		return
	}
	r.strategies[s.Name()] = s
}

// Order returns the ordered list of providers to attempt for a request.
//
// Eligibility is derived from the merchant's routing configuration. When the
// merchant has no explicit provider list (common for local development), every
// registered provider that supports the requested method/channel is used.
func (r *Router) Order(merchant *domain.Merchant, req *RouteRequest) ([]Candidate, error) {
	candidates := r.candidates(merchant, req)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: merchant=%s method=%s channel=%s",
			domain.ErrNoRoute, merchant.ID, req.Method, req.Channel)
	}
	strategy := r.strategy(merchant.Routing.Strategy)
	return strategy.Order(candidates, req), nil
}

// OrderForProvider returns a single-candidate list for an explicitly forced
// provider, validating that it exists and supports the request.
func (r *Router) OrderForProvider(name string, req *RouteRequest) ([]Candidate, error) {
	p, ok := r.registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: provider %q is not registered", domain.ErrNoRoute, name)
	}
	caps := p.Capabilities()
	if !caps.SupportsMethod(req.Method) {
		return nil, fmt.Errorf("%w: provider %q does not support method %s", domain.ErrNoRoute, name, req.Method)
	}
	if !caps.SupportsChannel(req.Channel) {
		return nil, fmt.Errorf("%w: provider %q does not support channel %s", domain.ErrNoRoute, name, req.Channel)
	}
	return []Candidate{{Provider: p, Priority: 0, Position: 0}}, nil
}

func (r *Router) strategy(name string) Strategy {
	if s, ok := r.strategies[name]; ok {
		return s
	}
	if r.fallback == nil {
		return &PriorityStrategy{}
	}
	return r.fallback
}

func (r *Router) candidates(merchant *domain.Merchant, req *RouteRequest) []Candidate {
	cfg := merchant.Routing.Providers

	// No explicit configuration: consider every registered provider.
	if len(cfg) == 0 {
		var out []Candidate
		for _, name := range r.registry.Names() {
			p, _ := r.registry.Get(name)
			caps := p.Capabilities()
			if !caps.SupportsMethod(req.Method) || !caps.SupportsChannel(req.Channel) {
				continue
			}
			out = append(out, Candidate{Provider: p, Position: len(out)})
		}
		return out
	}

	var out []Candidate
	for i, rp := range cfg {
		if !rp.Enabled {
			continue
		}
		p, ok := r.registry.Get(rp.Name)
		if !ok {
			continue
		}
		caps := p.Capabilities()
		if !caps.SupportsMethod(req.Method) || !caps.SupportsChannel(req.Channel) {
			continue
		}
		out = append(out, Candidate{
			Provider: p,
			Priority: rp.Priority,
			Weight:   rp.Weight,
			Position: i,
		})
	}
	return out
}
