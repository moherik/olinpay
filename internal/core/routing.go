package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/pkg/provider"
)

// RouteRequest is the subset of a payment request that routing cares about.
type RouteRequest struct {
	// MerchantID scopes stateful strategies (e.g. round-robin counters).
	MerchantID string
	// Environment is the isolation level the payment belongs to. It keeps
	// sandbox traffic on sandbox-registered provider instances and production
	// traffic on production instances.
	Environment string
	// Method is the unified payment method being requested.
	Method provider.Method
	// Channel optionally narrows the instrument.
	Channel string
	// Amount and Currency are available to amount-based strategies.
	Amount   provider.Amount
	Currency provider.Currency
	// Metadata is the request's metadata for metadata-based conditions.
	Metadata map[string]string
	// Description is the request description, exposed so routing can enrich it.
	Description string
}

// Candidate is a provider eligible to serve a request, together with the
// routing metadata that came from the merchant's configuration.
type Candidate struct {
	Provider provider.Provider
	Name     string
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

// instanceMatchesEnvironment reports whether a provider instance name may
// serve the given environment. Environment-suffixed instances ("winpay.sandbox",
// "winpay.production") are locked to their environment; unsuffixed legacy
// instances (environment-variable configured providers) serve both. Requests
// without an environment are pre-environment legacy traffic and see everything.
func instanceMatchesEnvironment(name, env string) bool {
	if env == "" {
		return true
	}
	if suffix := "." + env; strings.HasSuffix(name, suffix) {
		return true
	}
	// Unsuffixed names are legacy environment-variable configured instances
	// and serve every environment.
	return !strings.HasSuffix(name, "."+domain.EnvironmentSandbox) && !strings.HasSuffix(name, "."+domain.EnvironmentProduction)
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

// RouteDecision is the outcome of routing one request: the ordered provider
// candidates plus the request edits (enrichment and rule modifications) to
// apply before calling the provider.
type RouteDecision struct {
	Candidates []Candidate
	// Metadata is the effective request metadata after enrichment and rule
	// modifications. Nil means "keep the request's own metadata".
	Metadata map[string]string
	// Description is the effective description; empty means "keep the
	// request's own description".
	Description string
}

// Decide evaluates the merchant's routing configuration for a request.
//
// Conditional rules are evaluated first: the first rule matching the request
// prepends its provider chain ahead of the merchant's base chain, so the rule
// route is tried first and the configured fallback still catches a failure
// there. A matching rule with a Reject answer terminates routing immediately.
// When the merchant has no explicit provider list (common for local
// development), every registered provider that serves the request's
// environment is used.
func (r *Router) Decide(merchant *domain.Merchant, req *RouteRequest) (*RouteDecision, error) {
	// Enrichment runs before rule evaluation so metadata conditions read the
	// enriched values. The request copy is not mutated in place: the caller's
	// metadata map may be shared with the stored command.
	edits := applyModifies(merchant.Routing.Enrich, req.Metadata, req.Description)
	req = &RouteRequest{MerchantID: req.MerchantID, Environment: req.Environment, Method: req.Method, Channel: req.Channel, Amount: req.Amount, Currency: req.Currency}
	req.Metadata = edits.metadata
	req.Description = edits.description

	decision := &RouteDecision{Metadata: edits.metadata, Description: edits.description}

	for i := range merchant.Routing.Rules {
		rule := &merchant.Routing.Rules[i]
		if !conditionMatches(rule.Condition, req) {
			continue
		}
		if rule.Reject != nil {
			return nil, &domain.RejectedError{Status: rule.Reject.Status, Code: rule.Reject.Code, Message: rule.Reject.Message}
		}
		ruleEdits := applyModifies(rule.Apply, edits.metadata, edits.description)
		decision.Metadata = ruleEdits.metadata
		decision.Description = ruleEdits.description
		req.Metadata = ruleEdits.metadata
		req.Description = ruleEdits.description

		header := r.eligibleNames(rule.Providers, req)
		candidates := r.candidatesOf(header, req)
		if len(candidates) == 0 {
			break // rule matched but nothing eligible: fall through to the base chain
		}
		base := r.candidates(merchant, req)
		candidates = append(candidates, base...)
		decision.Candidates = r.strategy(merchant.Routing.Strategy).Order(candidates, req)
		return decision, nil
	}

	candidates := r.candidates(merchant, req)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: merchant=%s method=%s channel=%s",
			domain.ErrNoRoute, merchant.ID, req.Method, req.Channel)
	}
	decision.Candidates = r.strategy(merchant.Routing.Strategy).Order(candidates, req)
	return decision, nil
}

// routeEdits is the mutable accumulator for request modifications.
type routeEdits struct {
	metadata    map[string]string
	description string
}

// applyModifies applies modification ops onto a copy of the request's
// metadata; an empty result keeps the caller's original values.
func applyModifies(modifies []domain.RoutingModify, metadata map[string]string, description string) routeEdits {
	edits := routeEdits{metadata: metadata, description: description}
	cloned := false
	ensureClone := func() {
		if cloned {
			return
		}
		clone := make(map[string]string, len(metadata)+2)
		for k, v := range metadata {
			clone[k] = v
		}
		edits.metadata = clone
		cloned = true
	}
	for _, m := range modifies {
		switch m.Op {
		case domain.ModifyOpSetMetadata:
			if m.Key == "" {
				continue
			}
			ensureClone()
			edits.metadata[m.Key] = m.Value
		case domain.ModifyOpSetDescription:
			if m.Value == "" {
				continue
			}
			edits.description = m.Value
		}
	}
	return edits
}

// eligibleNames filters a configured provider chain down to registered
// instances that serve the request's environment.
func (r *Router) eligibleNames(names []string, req *RouteRequest) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" || !instanceMatchesEnvironment(name, req.Environment) {
			continue
		}
		if _, ok := r.registry.Get(name); !ok {
			continue
		}
		out = append(out, name)
	}
	return out
}

// candidatesOf builds candidates from instance names in list order; the
// position/priority inside the rule chain is the rule's list order.
func (r *Router) candidatesOf(names []string, req *RouteRequest) []Candidate {
	out := make([]Candidate, 0, len(names))
	for _, name := range names {
		p, ok := r.registry.Get(name)
		if !ok {
			continue
		}
		caps := p.Capabilities()
		if !caps.SupportsMethod(req.Method) || !caps.SupportsChannel(req.Channel) {
			continue
		}
		out = append(out, Candidate{Provider: p, Name: name, Priority: len(out), Position: len(out)})
	}
	return out
}

// conditionMatches evaluates a rule condition against the request. A missing
// metadata key never matches — a rule must not fire on requests that do not
// carry the data it routes on.
func conditionMatches(cond domain.RoutingCondition, req *RouteRequest) bool {
	if cond.Field == domain.ConditionFieldAmount {
		target, err := strconv.ParseInt(cond.Value, 10, 64)
		if err != nil {
			return false
		}
		switch cond.Operator {
		case domain.ConditionOperatorEq:
			return req.Amount == target
		case domain.ConditionOperatorNotEq:
			return req.Amount != target
		case domain.ConditionOperatorGte:
			return req.Amount >= target
		case domain.ConditionOperatorLte:
			return req.Amount <= target
		default:
			return false
		}
	}

	var actual string
	field := cond.Field
	switch {
	case field == domain.ConditionFieldMethod:
		actual = string(req.Method)
	case field == domain.ConditionFieldChannel:
		actual = req.Channel
	case field == domain.ConditionFieldCurrency:
		actual = string(req.Currency)
	case strings.HasPrefix(field, domain.ConditionMetadataPrefix):
		key := strings.TrimPrefix(field, domain.ConditionMetadataPrefix)
		got, present := req.Metadata[key]
		if !present {
			return false
		}
		actual = got
	default:
		return false
	}

	switch cond.Operator {
	case domain.ConditionOperatorEq:
		return actual == cond.Value
	case domain.ConditionOperatorNotEq:
		return actual != cond.Value
	default:
		return false
	}
}

// OrderForProvider returns a single-candidate list for an explicitly forced
// provider, validating that it exists and supports the request.
//
// The name may be an exact instance name (e.g. "winpay.sandbox") or a bare
// adapter name (e.g. "winpay"); a bare adapter name resolves to the instance
// registered for the request's environment. The resolved instance must match
// the request's environment.
func (r *Router) OrderForProvider(name string, req *RouteRequest) ([]Candidate, error) {
	resolved := name
	p, ok := r.registry.Get(resolved)
	if !ok && req.Environment != "" {
		resolved = domain.InstanceName(name, req.Environment)
		p, ok = r.registry.Get(resolved)
	}
	if !ok {
		return nil, fmt.Errorf("%w: provider %q is not registered", domain.ErrNoRoute, name)
	}
	if !instanceMatchesEnvironment(resolved, req.Environment) {
		return nil, fmt.Errorf("%w: provider %q is not available for the %s environment", domain.ErrNoRoute, name, req.Environment)
	}
	caps := p.Capabilities()
	if !caps.SupportsMethod(req.Method) {
		return nil, fmt.Errorf("%w: provider %q does not support method %s", domain.ErrNoRoute, name, req.Method)
	}
	if !caps.SupportsChannel(req.Channel) {
		return nil, fmt.Errorf("%w: provider %q does not support channel %s", domain.ErrNoRoute, name, req.Channel)
	}
	return []Candidate{{Provider: p, Name: resolved, Priority: 0, Position: 0}}, nil
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
	eligible := func(p provider.Provider) bool {
		caps := p.Capabilities()
		if !caps.SupportsMethod(req.Method) || !caps.SupportsChannel(req.Channel) {
			return false
		}
		return true
	}

	cfg := merchant.Routing.Providers

	// No explicit configuration: consider every registered provider that
	// serves the request's environment.
	if len(cfg) == 0 {
		var out []Candidate
		for _, name := range r.registry.Names() {
			if !instanceMatchesEnvironment(name, req.Environment) {
				continue
			}
			p, _ := r.registry.Get(name)
			if !eligible(p) {
				continue
			}
			out = append(out, Candidate{Provider: p, Name: name, Position: len(out)})
		}
		return out
	}

	var out []Candidate
	for i, rp := range cfg {
		if !rp.Enabled {
			continue
		}
		if !instanceMatchesEnvironment(rp.Name, req.Environment) {
			continue
		}
		p, ok := r.registry.Get(rp.Name)
		if !ok || !eligible(p) {
			continue
		}
		out = append(out, Candidate{
			Provider: p,
			Name:     rp.Name,
			Priority: rp.Priority,
			Weight:   rp.Weight,
			Position: i,
		})
	}
	return out
}
