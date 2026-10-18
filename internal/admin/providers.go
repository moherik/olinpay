package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/pkg/provider"
)

// listProviders serves GET /api/providers. Each catalog row carries, per
// environment: whether credentials are stored, whether an instance is live,
// which non-secret values are set, and the last update.
//
// Secret values never round-trip: configured secrets are reported only as
// "configured: true".
func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	if _, ok := resolveEnvironment(w, r.URL.Query().Get("env")); !ok {
		return
	}

	cfgRows, err := s.providerCfgs.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot list provider configs")
		return
	}
	stored := map[string]map[string]string{} // (adapter/env) -> stored config
	updated := map[string]time.Time{}
	updatedBy := map[string]string{}
	for _, row := range cfgRows {
		key := instanceKey(row.Provider, row.Environment)
		stored[key] = row.Config
		updated[key] = row.UpdatedAt
		updatedBy[key] = row.UpdatedBy
	}

	instances := map[string]bool{}
	for _, name := range s.registry.Names() {
		instances[name] = true
	}

	adapters := s.adaptersFor()
	out := make([]map[string]any, 0, len(adapters))
	for _, a := range adapters {
		envs := map[string]any{}
		for _, env := range []string{domain.EnvironmentSandbox, domain.EnvironmentProduction} {
			key := instanceKey(a.Name, env)
			cfg := stored[key]

			view := map[string]string{}
			for _, f := range a.Fields {
				if f.Secret {
					continue
				}
				view[f.Key] = cfg[f.Key]
			}
			configuredSecrets := []string{}
			known := map[string]bool{}
			for _, f := range a.Fields {
				known[f.Key] = true
				if f.Secret && cfg[f.Key] != "" {
					configuredSecrets = append(configuredSecrets, f.Key)
				}
			}
			customKeys := []string{}
			for k := range cfg {
				if !known[k] {
					customKeys = append(customKeys, k)
				}
			}
			sort.Strings(customKeys)

			envs[env] = map[string]any{
				"configured":  cfg != nil,
				"instance":    domain.InstanceName(a.Name, env),
				"active":      instances[domain.InstanceName(a.Name, env)],
				"values":      view,
				"set_secrets": configuredSecrets,
				"custom_keys": customKeys,
				"updated_at":  nilableTime(updated[key]),
				"updated_by":  updatedBy[key],
			}
		}
		out = append(out, map[string]any{
			"name":          a.Name,
			"label":         a.Label,
			"summary":       a.Summary,
			"environments":  a.Environments,
			"methods":       a.Methods,
			"config_fields": a.Fields,
			"env_state":     envs,
		})
	}
	writeData(w, http.StatusOK, out, map[string]any{"count": len(out)})
}

type saveProviderConfigRequest struct {
	Environment string          `json:"environment"`
	Config      json.RawMessage `json:"config"`
}

// saveProviderConfig serves PUT /api/providers/{name}/config.
//
// Merge semantics: non-secret provided fields overwrite; secret fields left
// empty keep their stored value (the console cannot echo secrets back, so
// "leave blank to keep" is the only workable editing model). Unknown keys are
// passed through so adapters can grow configuration without console changes.
//
// The provider is only persisted after the adapter factory accepted the full
// configuration — a 200 from here always means a live, validated instance.
func (s *Server) saveProviderConfig(w http.ResponseWriter, r *http.Request) {
	adapter := chiURLParam(r, "name")
	var body saveProviderConfigRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	env, ok := resolveEnvironment(w, body.Environment)
	if !ok {
		return
	}
	provided, err := mapConfig(body.Config)
	if err != nil {
		invalidField(w, "config", err.Error())
		return
	}

	fields := configFieldsFor(adapter)

	// Merge onto the stored row. Blank secret fields mean "keep current".
	merged, err := s.providerCfgs.Get(r.Context(), adapter, env)
	if err != nil {
		merged = &domain.ProviderConfig{Provider: adapter, Environment: env, Config: map[string]string{}}
	}
	for key, value := range provided {
		if isSecret(fields, key) && strings.TrimSpace(value) == "" {
			continue
		}
		merged.Config[key] = strings.TrimSpace(value)
	}
	for _, f := range fields {
		if f.Required && strings.TrimSpace(merged.Config[f.Key]) == "" {
			invalidField(w, "config", fmt.Sprintf("field %q is required", f.Key))
			return
		}
	}

	// Build before persisting: an adapter that rejects its config must not
	// leave a stored credential row that would load forever.
	if _, err := s.registry.BuildAs(domain.InstanceName(adapter, env), adapter, provider.Config(merged.Config)); err != nil {
		invalidConfigResponse(w, err)
		return
	}

	now := s.now()
	merged.UpdatedAt = now
	var actingAs string
	if actor, ok := userFromContext(r.Context()); ok {
		actingAs = actor.Email
	}
	merged.UpdatedBy = actingAs
	if err := s.providerCfgs.Upsert(r.Context(), merged); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot store provider config")
		return
	}
	if actingAs != "" {
		if actor, _ := userFromContext(r.Context()); actor != nil {
			s.auditWrite(r, actor, "provider.config_save", adapter+" "+env)
		}
	}
	if s.reload != nil {
		if err := s.reload(adapter, env); err != nil {
			// The instance is built; only the receiver's environment hints
			// failed to refresh. Not fatal, worth surfacing.
			writeData(w, http.StatusOK, stateFor(r.Context(), s, adapter, env), map[string]any{"saved": true, "reload_note": err.Error()})
			return
		}
	}
	writeData(w, http.StatusOK, stateFor(r.Context(), s, adapter, env), map[string]any{"saved": true})
}

// removeProviderConfig serves DELETE /api/providers/{name}/config?env=. The
// stored row is dropped and the reload hook decides what the instance becomes
// (environment variables may still own the same instance name).
func (s *Server) removeProviderConfig(w http.ResponseWriter, r *http.Request) {
	adapter := chiURLParam(r, "name")
	env, ok := resolveEnvironment(w, r.URL.Query().Get("env"))
	if !ok {
		return
	}
	if env == "" {
		invalidField(w, "env", "environment is required")
		return
	}

	if err := s.providerCfgs.Delete(r.Context(), adapter, env); err != nil {
		notFound(w, "provider config")
		return
	}
	if s.reload != nil {
		_ = s.reload(adapter, env)
	}
	if actor, ok := userFromContext(r.Context()); ok {
		s.auditWrite(r, actor, "provider.config_remove", adapter+" "+env)
	}
	writeData(w, http.StatusOK, map[string]any{"removed": true}, map[string]any{"removed": true})
}

// configFieldsFor returns the catalog fields of an adapter (empty slice for
// adapters outside the static catalog).
func configFieldsFor(adapter string) []Field {
	a, ok := adapterCatalog[adapter]
	if !ok {
		return nil
	}
	return a.Fields
}

func isSecret(fields []Field, key string) bool {
	for _, f := range fields {
		if f.Key == key {
			return f.Secret
		}
	}
	return false
}

// stateFor is the post-save snapshot the console shows immediately.
func stateFor(ctx context.Context, s *Server, adapter, env string) map[string]any {
	stored, _ := s.providerCfgs.Get(ctx, adapter, env)
	instance := domain.InstanceName(adapter, env)
	_, active := s.registry.Get(instance)
	configured := stored != nil
	secrets := []string{}
	for k, v := range storedConfig(stored) {
		if v != "" && isSecret(configFieldsFor(adapter), k) {
			secrets = append(secrets, k)
		}
	}
	return map[string]any{
		"provider":    adapter,
		"environment": env,
		"configured":  configured,
		"active":      active,
		"instance":    instance,
		"set_secrets": secrets,
		"updated_at":  nilableTime(updatedAtOf(stored)),
		"updated_by":  updatedByOf(stored),
	}
}

func storedConfig(pc *domain.ProviderConfig) map[string]string {
	if pc == nil {
		return nil
	}
	return pc.Config
}

func updatedAtOf(pc *domain.ProviderConfig) time.Time {
	if pc == nil {
		return time.Time{}
	}
	return pc.UpdatedAt
}

func updatedByOf(pc *domain.ProviderConfig) string {
	if pc == nil {
		return ""
	}
	return pc.UpdatedBy
}

func nilableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func instanceKey(provider, env string) string { return provider + "/" + env }

// httpxInvalidConfig renders an adapter's build rejection as a 400.
func invalidConfigResponse(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadRequest, "invalid_config", err.Error())
}
