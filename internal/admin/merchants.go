package admin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/id"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/pkg/provider"
)

// listMerchants serves GET /api/merchants?q= with per-merchant card data: key
// counts, 30-day transaction volume and the environments the merchant holds
// keys in.
func (s *Server) listMerchants(w http.ResponseWriter, r *http.Request) {
	listings, err := s.stats.MerchantsWithStats(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot list merchants")
		return
	}

	type merchantRow struct {
		*domain.Merchant
		ActiveKeys      int      `json:"active_keys"`
		Transactions30d int      `json:"transactions_30d"`
		Environments    []string `json:"environments"`
	}
	rows := make([]merchantRow, 0, len(listings))
	for _, l := range listings {
		envs, err := s.apiKeys.EnvironmentsOfMerchant(r.Context(), l.Merchant.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "cannot list merchant environments")
			return
		}
		rows = append(rows, merchantRow{
			Merchant:        l.Merchant,
			ActiveKeys:      l.ActiveKeys,
			Transactions30d: l.Transactions30d,
			Environments:    envs,
		})
	}
	writeData(w, http.StatusOK, rows, map[string]any{"count": len(rows)})
}

// getMerchant serves GET /api/merchants/{id} with the counts the detail page
// header shows.
func (s *Server) getMerchant(w http.ResponseWriter, r *http.Request) {
	m, err := s.merchants.GetByID(r.Context(), chiURLParam(r, "id"))
	if err != nil {
		notFound(w, "merchant")
		return
	}
	envs, _ := s.apiKeys.EnvironmentsOfMerchant(r.Context(), m.ID)
	writeData(w, http.StatusOK, m, map[string]any{"environments": envs})
}

// createMerchant serves POST /api/merchants {name, callback_url}.
//
// New merchants have no legacy credential: they get their keys from the
// console's API-keys section (merchants.api_key_hash stays NULL). A fresh
// webhook secret is generated unconditionally — it signs outbound callbacks.
func (s *Server) createMerchant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		CallbackURL string `json:"callback_url"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		invalidField(w, "name", "name is required")
		return
	}
	merchantID, err := id.NewMerchantID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot generate merchant id")
		return
	}
	secret, err := newWebhookSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot generate webhook secret")
		return
	}
	now := s.now()
	m := &domain.Merchant{
		ID:            merchantID,
		Name:          name,
		CallbackURL:   strings.TrimSpace(body.CallbackURL),
		WebhookSecret: secret,
		Status:        domain.MerchantActive,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.merchants.Create(r.Context(), m); err != nil {
		s.writeEngineError(w, err, "cannot create merchant")
		return
	}

	if user, ok := userFromContext(r.Context()); ok {
		s.auditWrite(r, user, "merchant.create", m.ID+" "+m.Name)
	}
	writeData(w, http.StatusCreated, m, map[string]any{
		// The secret is shown once so the console can hand it to the merchant.
		"webhook_secret": secret,
	})
}

// updateMerchant serves PATCH /api/merchants/{id} {name?, callback_url?,
// status?}. Status changes go through the same lifecycle the core engine
// enforces: suspended/inactive merchants fail authentication.
func (s *Server) updateMerchant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        *string `json:"name"`
		CallbackURL *string `json:"callback_url"`
		Status      *string `json:"status"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	m, err := s.merchants.GetByID(r.Context(), chiURLParam(r, "id"))
	if err != nil {
		notFound(w, "merchant")
		return
	}

	statusChanged := ""
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" {
		m.Name = strings.TrimSpace(*body.Name)
	}
	if body.CallbackURL != nil {
		m.CallbackURL = strings.TrimSpace(*body.CallbackURL)
	}
	if body.Status != nil {
		switch domain.MerchantStatus(*body.Status) {
		case domain.MerchantActive, domain.MerchantInactive, domain.MerchantSuspended:
			if m.Status != domain.MerchantStatus(*body.Status) {
				m.Status = domain.MerchantStatus(*body.Status)
				statusChanged = *body.Status
			}
		default:
			invalidField(w, "status", "status must be active, inactive or suspended")
			return
		}
	}
	m.UpdatedAt = s.now()

	if err := s.merchants.Update(r.Context(), m); err != nil {
		s.writeEngineError(w, err, "cannot update merchant")
		return
	}

	if user, ok := userFromContext(r.Context()); ok {
		if statusChanged != "" {
			s.auditWrite(r, user, "merchant.status", m.ID+" "+statusChanged)
		}
		s.auditWrite(r, user, "merchant.update", m.ID)
	}

	writeData(w, http.StatusOK, m, map[string]any{
		"environments":   envsOf(s, r, m.ID),
		"status_changed": statusChanged,
	})
}

// envsOf is a best-effort helper; the environments tag set is decorative.
func envsOf(s *Server, r *http.Request, merchantID string) []string {
	envs, err := s.apiKeys.EnvironmentsOfMerchant(r.Context(), merchantID)
	if err != nil {
		return nil
	}
	return envs
}

// saveRouting serves PUT /api/merchants/{id}/routing. The body replaces the
// merchant's routing configuration wholesale; provider instance names carry
// the environment (e.g. "xendit" production, "xendit.sandbox" sandbox), so
// sandbox and production traffic can follow different chains inside one
// configuration.
func (s *Server) saveRouting(w http.ResponseWriter, r *http.Request) {
	var body domain.RoutingConfig
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	if body.Strategy != "" && body.Strategy != domain.StrategyPriority && body.Strategy != domain.StrategyRoundRobin {
		invalidField(w, "strategy", "strategy must be priority or round_robin")
		return
	}
	seen := map[string]bool{}
	for i, p := range body.Providers {
		if strings.TrimSpace(p.Name) == "" {
			invalidField(w, "providers", fmt.Sprintf("provider %d has no name", i))
			return
		}
		if seen[p.Name] {
			invalidField(w, "providers", "duplicate provider "+p.Name)
			return
		}
		seen[p.Name] = true
		// Every stored entry must reference an instance we can actually
		// dispatch to. Ordering a provider without credentials would push
		// production traffic into dead routes at runtime.
		if _, ok := s.registry.Get(p.Name); !ok {
			invalidField(w, "providers", "provider "+p.Name+" is not registered")
			return
		}
		p.Priority = i
		body.Providers[i] = p
	}
	if err := validateRoutingRules(body.Rules, s.registry); err != nil {
		invalidField(w, "rules", err.Error())
		return
	}
	if err := validateModifies(body.Enrich, "enrich"); err != nil {
		invalidField(w, "enrich", err.Error())
		return
	}
	if err := validateReject(body.Reject, "reject"); err != nil {
		invalidField(w, "reject", err.Error())
		return
	}
	if body.Rules == nil {
		body.Rules = []domain.RoutingRule{}
	}

	m, err := s.merchants.GetByID(r.Context(), chiURLParam(r, "id"))
	if err != nil {
		notFound(w, "merchant")
		return
	}
	m.Routing = body
	m.UpdatedAt = s.now()
	if err := s.merchants.Update(r.Context(), m); err != nil {
		s.writeEngineError(w, err, "cannot save routing")
		return
	}

	if user, ok := userFromContext(r.Context()); ok {
		names := make([]string, 0, len(body.Providers))
		for _, p := range body.Providers {
			names = append(names, p.Name)
		}
		detail := m.ID + " [" + strings.Join(names, " → ") + "]"
		if len(body.Rules) > 0 {
			detail += fmt.Sprintf(", %d conditional rules", len(body.Rules))
		}
		s.auditWrite(r, user, "merchant.routing", detail)
	}
	writeData(w, http.StatusOK, m, map[string]any{
		"environments": envsOf(s, r, m.ID),
	})
}

// validateRoutingRules checks conditional rules before they are stored: a
// rule whose condition is structurally impossible would silently never fire,
// and a rule chain referencing an unregistered instance would fail at match
// time. Amount compares the request's integer amount as-is (IDR: rupiah).
func validateRoutingRules(rules []domain.RoutingRule, registry *provider.Registry) error {
	allowedStrOps := fmt.Sprintf("%s, %s", domain.ConditionOperatorEq, domain.ConditionOperatorNotEq)
	allowedNumOps := allowedStrOps + fmt.Sprintf(", %s, %s", domain.ConditionOperatorGte, domain.ConditionOperatorLte)
	seenKeys := map[string]bool{}
	for i, rule := range rules {
		label := fmt.Sprintf("rule %d", i+1)
		cond := rule.Condition
		if seenKeys[cond.Field+"|"+cond.Operator+"|"+cond.Value] {
			return fmt.Errorf("%s is a duplicate condition", label)
		}
		seenKeys[cond.Field+"|"+cond.Operator+"|"+cond.Value] = true

		field := cond.Field
		key, isMetadata := strings.CutPrefix(field, domain.ConditionMetadataPrefix)
		switch {
		case field == domain.ConditionFieldAmount:
			if _, err := strconv.ParseInt(cond.Value, 10, 64); err != nil {
				return fmt.Errorf("%s condition value %q is not an integer (IDR: rupiah)", label, cond.Value)
			}
			if _, ok := numOperator(cond.Operator); !ok {
				return fmt.Errorf("%s condition operator %q must be one of %s", label, cond.Operator, allowedNumOps)
			}
		case field == domain.ConditionFieldMethod, field == domain.ConditionFieldChannel, field == domain.ConditionFieldCurrency:
			if _, ok := stringOperator(cond.Operator); !ok {
				return fmt.Errorf("%s condition operator %q must be one of %s", label, cond.Operator, allowedStrOps)
			}
		case isMetadata && strings.TrimSpace(key) != "":
			if !metadataKeyPattern.MatchString(key) {
				return fmt.Errorf("%s metadata key %q may only contain letters, digits, '_' and '-'", label, key)
			}
			if _, ok := stringOperator(cond.Operator); !ok {
				return fmt.Errorf("%s condition operator %q must be one of %s", label, cond.Operator, allowedStrOps)
			}
		default:
			return fmt.Errorf("%s condition field must be amount, method, channel, currency or \"metadata:<key>\"", label)
		}
		if strings.TrimSpace(cond.Value) == "" {
			return fmt.Errorf("%s condition value is required", label)
		}
		if err := validateModifies(rule.Apply, label); err != nil {
			return err
		}
		if rule.Reject != nil {
			if len(rule.Providers) > 0 {
				return fmt.Errorf("%s cannot both route to providers and reject", label)
			}
			if err := validateReject(rule.Reject, label); err != nil {
				return err
			}
			continue
		}
		if len(rule.Providers) == 0 {
			return fmt.Errorf("%s routes to no provider — remove the rule or pick at least one", label)
		}
		seen := map[string]bool{}
		for _, name := range rule.Providers {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("%s references an empty provider", label)
			}
			if seen[name] {
				return fmt.Errorf("%s lists provider %s twice", label, name)
			}
			seen[name] = true
			if _, ok := registry.Get(name); !ok {
				return fmt.Errorf("%s references provider %s which is not registered", label, name)
			}
		}
	}
	return nil
}

func numOperator(op string) (string, bool) {
	switch op {
	case domain.ConditionOperatorEq, domain.ConditionOperatorNotEq,
		domain.ConditionOperatorGte, domain.ConditionOperatorLte:
		return op, true
	default:
		return "", false
	}
}

func stringOperator(op string) (string, bool) {
	switch op {
	case domain.ConditionOperatorEq, domain.ConditionOperatorNotEq:
		return op, true
	default:
		return "", false
	}
}

// validateModifies checks one routing modify list before it is stored. An
// operation the engine would silently skip (unknown op, missing metadata key)
// corrupts what the merchant believes routing does to the request.
func validateModifies(modifies []domain.RoutingModify, label string) error {
	for i, m := range modifies {
		what := fmt.Sprintf("%s modify %d", label, i+1)
		switch m.Op {
		case domain.ModifyOpSetMetadata:
			if strings.TrimSpace(m.Key) == "" {
				return fmt.Errorf("%s (set_metadata) needs a metadata key", what)
			}
			if !metadataKeyPattern.MatchString(m.Key) {
				return fmt.Errorf("%s metadata key %q may only contain letters, digits, '_' and '-'", what, m.Key)
			}
		case domain.ModifyOpSetDescription:
			if strings.TrimSpace(m.Value) == "" {
				return fmt.Errorf("%s (set_description) needs a value", what)
			}
		default:
			return fmt.Errorf("%s operation must be %s or %s", what, domain.ModifyOpSetMetadata, domain.ModifyOpSetDescription)
		}
	}
	return nil
}

// validateReject checks a routing reject definition. The status must be in the
// error range: a reject only ever answers a payment that is not accepted, so
// the API layer can safely trust the stored value to be 4xx/5xx.
func validateReject(rej *domain.RoutingReject, label string) error {
	if rej == nil {
		return nil
	}
	if rej.Status < http.StatusBadRequest || rej.Status > 599 {
		return fmt.Errorf("%s status must be 400..599", label)
	}
	code := strings.TrimSpace(rej.Code)
	if code == "" {
		return fmt.Errorf("%s code is required", label)
	}
	if strings.ContainsAny(code, " \t\r\n") || len(code) > 64 {
		return fmt.Errorf("%s code must be one word of at most 64 characters", label)
	}
	if strings.TrimSpace(rej.Message) == "" {
		return fmt.Errorf("%s message is required", label)
	}
	if len(strings.TrimSpace(rej.Message)) > 500 {
		return fmt.Errorf("%s message must be at most 500 characters", label)
	}
	return nil
}

// metadataKeyPattern restricts rule metadata keys to URL/JSON-friendly
// characters so the field name round-trips through queries and audit lines.
var metadataKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// newWebhookSecret generates the per-merchant outbound callback secret.
func newWebhookSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("admin: generating webhook secret: %w", err)
	}
	return "whsec_" + hex.EncodeToString(buf), nil
}

// --- API keys ---------------------------------------------------------------

// listAPIKeys serves GET /api/api-keys?env=&merchant_id=. The hash never
// leaves the server; the console operates on prefixes only.
func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	env, ok := resolveEnvironment(w, r.URL.Query().Get("env"))
	if !ok {
		return
	}
	f := repository.APIKeyFilter{
		Environment: env,
		MerchantID:  strings.TrimSpace(r.URL.Query().Get("merchant_id")),
		Limit:       parseInt(r.URL.Query().Get("limit"), 100),
	}
	keys, err := s.apiKeys.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot list API keys")
		return
	}
	writeData(w, http.StatusOK, keys, paginationMeta(f.Limit, f.Offset, len(keys)))
}

type createAPIKeyRequest struct {
	MerchantID  string `json:"merchant_id"`
	Label       string `json:"label"`
	Environment string `json:"environment"`
}

// createAPIKey serves POST /api/api-keys. The raw key is returned exactly
// once: only its salted hash and a short prefix persist.
func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var body createAPIKeyRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	env, ok := resolveEnvironment(w, body.Environment)
	if !ok {
		return
	}
	if strings.TrimSpace(body.MerchantID) == "" {
		invalidField(w, "merchant_id", "merchant_id is required")
		return
	}
	m, err := s.merchants.GetByID(r.Context(), body.MerchantID)
	if err != nil {
		notFound(w, "merchant")
		return
	}

	raw, err := newAPIKeyFor(env)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot generate API key")
		return
	}
	now := s.now()
	key := &domain.APIKey{
		ID:          s.newID("key"),
		MerchantID:  m.ID,
		Label:       strings.TrimSpace(body.Label),
		KeyHash:     id.HashAPIKey(raw, s.pepper),
		KeyPrefix:   domain.APIKeyPrefixFor(raw),
		Environment: env,
		Status:      domain.APIKeyActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.apiKeys.Create(r.Context(), key); err != nil {
		s.writeEngineError(w, err, "cannot create API key")
		return
	}

	if user, ok := userFromContext(r.Context()); ok {
		s.auditWrite(r, user, "api_key.create", key.ID+" "+env+" "+m.Name)
	}
	writeData(w, http.StatusCreated, key, map[string]any{
		// Shown once. Nothing in the system can recover it afterwards.
		"key": raw,
	})
}

// newAPIKeyFor builds a sk_test_* / sk_live_* credential. The prefix decides
// the environment at authentication time for legacy integrations too.
func newAPIKeyFor(env string) (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("admin: generating api key: %w", err)
	}
	prefix := "sk_test_"
	if env == domain.EnvironmentProduction {
		prefix = "sk_live_"
	}
	return prefix + hex.EncodeToString(buf), nil
}

// revokeAPIKey serves POST /api/api-keys/{id}/revoke. Revocation must be
// effective immediately: the merchant middleware consults the status column
// on every request, and revoked keys fail exactly like unknown ones.
func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	key, err := s.apiKeys.GetByID(r.Context(), chiURLParam(r, "id"))
	if err != nil {
		notFound(w, "API key")
		return
	}
	if key.Revoked() {
		writeData(w, http.StatusOK, key, map[string]any{"revoked": true})
		return
	}
	key.Status = domain.APIKeyRevoked
	key.UpdatedAt = s.now()
	if err := s.apiKeys.Update(r.Context(), key); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot revoke API key")
		return
	}

	if user, ok := userFromContext(r.Context()); ok {
		s.auditWrite(r, user, "api_key.revoke", key.ID+" "+key.Environment)
	}
	writeData(w, http.StatusOK, key, map[string]any{"revoked": true})
}
