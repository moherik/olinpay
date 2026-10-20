package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moherik/openpay.id/internal/admin"
	"github.com/moherik/openpay.id/internal/config"
	"github.com/moherik/openpay.id/internal/core"
	"github.com/moherik/openpay.id/internal/domain"
	"github.com/moherik/openpay.id/internal/platform/id"
	"github.com/moherik/openpay.id/internal/repository"
	"github.com/moherik/openpay.id/internal/testutil/memory"
	"github.com/moherik/openpay.id/internal/webhook"
	"github.com/moherik/openpay.id/pkg/provider"
	"github.com/moherik/openpay.id/pkg/provider/mock"
	"golang.org/x/crypto/bcrypt"
)

const testPepper = "pepper"

// fixture wires the whole admin surface against in-memory repositories and
// the mock gateway.
type fixture struct {
	server   *httptest.Server
	store    *memory.Store
	engine   *core.Engine
	registry *provider.Registry
}

func setup(t *testing.T) *fixture {
	t.Helper()

	gateway, err := mock.New(provider.Config{})
	if err != nil {
		t.Fatalf("mock.New: %v", err)
	}
	registry := provider.NewRegistry()
	registry.Add(gateway)

	store := memory.NewStore()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	merchant := &domain.Merchant{
		ID:          "AW9X2K4T7Z",
		Name:        "Console Merchant",
		CallbackURL: "https://example.test/callback",
		Status:      domain.MerchantActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := store.Merchants().Create(context.Background(), merchant); err != nil {
		t.Fatalf("seeding merchant: %v", err)
	}

	dispatcher := webhook.NewDispatcher(store.Webhooks(), store.Merchants(), config.WebhookConfig{}, quiet)
	engine := core.New(core.Deps{
		Registry:     registry,
		Router:       core.NewRouter(registry),
		Transactions: store.Transactions(),
		Merchants:    store.Merchants(),
		Notifier:     dispatcher,
		Logger:       quiet,
	})

	srv := admin.New(admin.Deps{
		Engine:       engine,
		Registry:     registry,
		AdminUsers:   store.AdminUsers(),
		Sessions:     store.AdminSessions(),
		Audit:        store.Audit(),
		APIKeys:      store.APIKeys(),
		Merchants:    store.Merchants(),
		Transactions: store.Transactions(),
		Deliveries:   store.Webhooks(),
		Inbound:      store.Inbound(),
		ProviderCfgs: store.ProviderConfigs(),
		Stats:        store.AdminStats(),
		ReloadProvider: func(name, env string) error {
			row, err := store.ProviderConfigs().Get(context.Background(), name, env)
			if err != nil {
				registry.Remove(domain.InstanceName(name, env))
				return nil
			}
			_, err = registry.BuildAs(domain.InstanceName(name, env), name, provider.Config(row.Config))
			return err
		},
		FeatureFlags: func() map[string]bool {
			return map[string]bool{"transactions": true, "routing": true, "refund": false}
		},
		WebhookMaxAttempts: 5,
		Pepper:             testPepper,
		Log:                quiet,
	})

	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return &fixture{server: httpSrv, store: store, engine: engine, registry: registry}
}

// seedUser inserts a console account with a bcrypt password.
func (f *fixture) seedUser(t *testing.T, email, password, role, status string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hashing password: %v", err)
	}
	u := &domain.AdminUser{
		ID:           id.NewPrefixed("usr"),
		Email:        email,
		Name:         email,
		PasswordHash: string(hash),
		Role:         domain.AdminRole(role),
		Status:       status,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := f.store.AdminUsers().Create(context.Background(), u); err != nil {
		t.Fatalf("seeding admin user: %v", err)
	}
}

// login posts credentials and returns the response cookie.
func (f *fixture) login(t *testing.T, email, password string) *http.Cookie {
	t.Helper()
	status, resp, body := f.do(t, "POST", "/api/login", map[string]string{"email": email, "password": password})
	if status != http.StatusOK {
		t.Fatalf("login %s: status %d: %s", email, status, body)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "openpay_admin_session" && c.Value != "" {
			return c
		}
	}
	t.Fatal("login returned no session cookie")
	return nil
}

// do performs a request without a session cookie and returns status,
// response and body.
func (f *fixture) do(t *testing.T, method, path string, body any) (int, *http.Response, []byte) {
	t.Helper()
	return f.doRaw(t, nil, method, path, body)
}

// doRaw performs a request with an optional session cookie.
func (f *fixture) doRaw(t *testing.T, cookie *http.Cookie, method, path string, body any) (int, *http.Response, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshalling body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	return res.StatusCode, res, out
}

// doWith runs the request with a session cookie and returns status plus the
// decoded envelope.
func (f *fixture) doWith(t *testing.T, cookie *http.Cookie, method, path string, body any) (int, map[string]any) {
	t.Helper()
	status, _, raw := f.doRaw(t, cookie, method, path, body)
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil && len(raw) > 0 {
		t.Fatalf("decoding response %s: %v (%s)", path, err, raw)
	}
	return status, envelope
}

// loginAs seeds the account, then signs in.
func (f *fixture) loginAs(t *testing.T, email, role string) *http.Cookie {
	t.Helper()
	f.seedUser(t, email, "correct horse", role, domain.AdminUserActive)
	return f.login(t, email, "correct horse")
}

// seedTransaction creates a transaction via the engine.
func (f *fixture) seedTransaction(t *testing.T, reference, env string) *domain.Transaction {
	t.Helper()
	tx, err := f.engine.CreatePayment(context.Background(), core.CreatePaymentCommand{
		MerchantID:  "AW9X2K4T7Z",
		Environment: env,
		ReferenceID: reference,
		Amount:      150000,
		Method:      provider.MethodVirtualAccount,
		Channel:     "MOCK_VA",
		Description: "console test",
	})
	if err != nil {
		t.Fatalf("creating payment: %v", err)
	}
	return tx
}

// --- authentication ---------------------------------------------------------

func TestLoginSuccessIssuesSessionCookie(t *testing.T) {
	f := setup(t)
	f.seedUser(t, "owner@console.test", "correct horse", "owner", domain.AdminUserActive)
	f.login(t, "owner@console.test", "correct horse")
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	f := setup(t)
	f.seedUser(t, "owner@console.test", "correct horse", "owner", domain.AdminUserActive)
	status, _, body := f.do(t, "POST", "/api/login", map[string]string{
		"email":    "owner@console.test",
		"password": "wrong",
	})
	if status != 401 {
		t.Fatalf("wrong password: status %d", status)
	}
	if !strings.Contains(string(body), "invalid_credentials") {
		t.Fatalf("wrong password body: %s", body)
	}
}

func TestLoginRejectsSuspendedUser(t *testing.T) {
	f := setup(t)
	f.seedUser(t, "owner@console.test", "correct horse", "owner", domain.AdminUserSuspended)
	status, _, _ := f.do(t, "POST", "/api/login", map[string]string{
		"email":    "owner@console.test",
		"password": "correct horse",
	})
	if status != 403 {
		t.Fatalf("suspended: status %d", status)
	}
}

func TestUnknownEmailRejected(t *testing.T) {
	f := setup(t)
	status, _, _ := f.do(t, "POST", "/api/login", map[string]string{
		"email":    "ghost@console.test",
		"password": "correct horse",
	})
	if status != 401 {
		t.Fatalf("unknown account: status %d", status)
	}
}

func TestRequireSessionBlocksAnonymous(t *testing.T) {
	f := setup(t)
	for _, path := range []string{
		"/api/me", "/api/overview", "/api/transactions", "/api/users",
		"/api/audit", "/api/providers", "/api/api-keys",
	} {
		status, _, _ := f.do(t, "GET", path, nil)
		if status != 401 {
			t.Fatalf("%s without session: status %d", path, status)
		}
	}
}

func TestMeReportsUserAndFlags(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "tech@console.test", "tech")
	status, envelope := f.doWith(t, cookie, "GET", "/api/me", nil)
	if status != 200 {
		t.Fatalf("me: status %d", status)
	}
	data := envelope["data"].(map[string]any)
	user := data["user"].(map[string]any)
	if user["role"] != "tech" {
		t.Fatalf("me role: %v", user)
	}
	flags := data["flags"].(map[string]any)
	if flags["refund"] != false {
		t.Fatalf("flags refund: %v", flags)
	}
}

func TestLogoutRevokesSessionServerSide(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")

	status, _ := f.doWith(t, cookie, "POST", "/api/logout", nil)
	if status != 200 {
		t.Fatalf("logout: status %d", status)
	}
	// The cookie must now be dead even if the client keeps it.
	status, _ = f.doWith(t, cookie, "GET", "/api/me", nil)
	if status != 401 {
		t.Fatalf("me after logout: status %d", status)
	}
}

// --- RBAC -------------------------------------------------------------------

func TestRBACRoleEnforcement(t *testing.T) {
	f := setup(t)

	cases := []struct {
		role     string
		method   string
		path     string
		body     any
		wantCode int
	}{
		{"tech", "POST", "/api/transactions/x/cancel", map[string]string{"reason": "cancel"}, 403},
		{"tech", "POST", "/api/users", map[string]string{"email": "a@b.co", "role": "tech"}, 403},
		{"tech", "PUT", "/api/providers/mock/config", map[string]any{}, 403},
		{"operator", "POST", "/api/users", map[string]string{"email": "a@b.co", "role": "tech"}, 403},
		{"operator", "PUT", "/api/providers/mock/config", map[string]any{}, 403},
		{"owner", "POST", "/api/users", map[string]string{"email": "x@console.test", "role": "tech"}, 201},
	}

	tx := f.seedTransaction(t, "RBAC-1", domain.EnvironmentSandbox)
	for i, tc := range cases {
		cookie := f.loginAs(t, tc.role+".rbac"+string(rune('a'+i))+"@console.test", tc.role)
		path := strings.Replace(tc.path, "/transactions/x/", "/transactions/"+tx.ID+"/", 1)
		status, _ := f.doWith(t, cookie, tc.method, path, tc.body)
		if status != tc.wantCode {
			t.Fatalf("%s %s as %s: status %d want %d", tc.method, path, tc.role, status, tc.wantCode)
		}
	}
}

func TestTechCanReadEverything(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "tech@console.test", "tech")
	for _, path := range []string{
		"/api/me", "/api/overview", "/api/transactions", "/api/deliveries",
		"/api/inbound", "/api/merchants", "/api/api-keys", "/api/users", "/api/audit",
	} {
		status, _ := f.doWith(t, cookie, "GET", path, nil)
		if status != 200 {
			t.Fatalf("%s as tech: status %d", path, status)
		}
	}
}

// --- transactions -----------------------------------------------------------

func TestListTransactionsFiltersByEnvironment(t *testing.T) {
	f := setup(t)
	f.seedTransaction(t, "ENV-S1", domain.EnvironmentSandbox)
	f.seedTransaction(t, "ENV-P1", domain.EnvironmentProduction)

	cookie := f.loginAs(t, "owner@console.test", "owner")

	status, sandbox := f.doWith(t, cookie, "GET", "/api/transactions?env=sandbox", nil)
	if status != 200 {
		t.Fatalf("list sandbox: status %d", status)
	}
	rows := sandbox["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("sandbox rows: %d", len(rows))
	}
	if id := rows[0].(map[string]any)["reference_id"]; id != "ENV-S1" {
		t.Fatalf("sandbox first row: %v", id)
	}

	status, all := f.doWith(t, cookie, "GET", "/api/transactions", nil)
	if status != 200 {
		t.Fatalf("list all: status %d", status)
	}
	if rows := all["data"].([]any); len(rows) != 2 {
		t.Fatalf("all rows: %d", len(rows))
	}
}

func TestGetTransactionMetaReportsCapabilities(t *testing.T) {
	f := setup(t)
	tx := f.seedTransaction(t, "META-1", domain.EnvironmentSandbox)

	cookie := f.loginAs(t, "tech@console.test", "tech")
	status, data := f.doWith(t, cookie, "GET", "/api/transactions/"+tx.ID, nil)
	if status != 200 {
		t.Fatalf("get: status %d", status)
	}
	meta := data["meta"].(map[string]any)
	if meta["can_cancel"] != true {
		t.Fatalf("can_cancel: %v (mock supports cancel)", meta)
	}
	if meta["can_sync"] != true {
		t.Fatalf("can_sync: %v (mock supports inquiry)", meta)
	}
}

func TestCancelTransaction(t *testing.T) {
	f := setup(t)
	tx := f.seedTransaction(t, "CANCEL-1", domain.EnvironmentSandbox)

	cookie := f.loginAs(t, "operator@console.test", "operator")
	status, data := f.doWith(t, cookie, "POST", "/api/transactions/"+tx.ID+"/cancel", map[string]string{"reason": "cancel"})
	if status != 200 {
		body := data["error"]
		t.Fatalf("cancel: status %d body %v", status, body)
	}
	updated := data["data"].(map[string]any)
	if updated["status"] != string(provider.StatusCancelled) {
		t.Fatalf("cancelled status: %v", updated["status"])
	}

	entries, err := f.store.Audit().List(context.Background(), repository.AuditFilter{Search: "transaction.cancel"})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries after cancel: %d entries", len(entries))
	}
	if entries[0].Action != "transaction.cancel" {
		t.Fatalf("audit action: %s", entries[0].Action)
	}
}

func TestCancelTerminalTransactionFails(t *testing.T) {
	f := setup(t)
	tx := f.seedTransaction(t, "CANCEL-2", domain.EnvironmentSandbox)
	if _, err := f.engine.HandleProviderEvent(context.Background(), "mock", tx.Environment, &provider.WebhookEvent{
		ReferenceID: tx.ReferenceID,
		Status:      provider.StatusPaid,
		Amount:      150000,
	}); err != nil {
		t.Fatalf("paying tx: %v", err)
	}

	cookie := f.loginAs(t, "owner@console.test", "owner")
	status, _ := f.doWith(t, cookie, "POST", "/api/transactions/"+tx.ID+"/cancel", nil)
	if status != 422 {
		t.Fatalf("cancel paid tx: status %d", status)
	}
}

// --- API keys ---------------------------------------------------------------

func TestAPIKeyLifecycle(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")

	status, data := f.doWith(t, cookie, "POST", "/api/api-keys", map[string]string{
		"merchant_id": "AW9X2K4T7Z",
		"label":       "hq",
		"environment": "production",
	})
	if status != 201 {
		t.Fatalf("create key: status %d", status)
	}
	raw := data["meta"].(map[string]any)["key"].(string)
	if !strings.HasPrefix(raw, "sk_live_") {
		t.Fatalf("production key prefix: %s", raw)
	}
	key := data["data"].(map[string]any)

	// Only the hash persists; the raw key must not be recoverable anywhere.
	listed, _ := f.store.APIKeys().GetByHash(context.Background(), id.HashAPIKey(raw, testPepper))
	if listed == nil {
		t.Fatal("created key not resolvable by hash")
	}
	if listed.ID != key["id"].(string) {
		t.Fatal("created key id mismatch")
	}

	// Revocation is effective on the stored row immediately.
	status, _ = f.doWith(t, cookie, "POST", "/api/api-keys/"+listed.ID+"/revoke", nil)
	if status != 200 {
		t.Fatalf("revoke: status %d", status)
	}
	revoked, _ := f.store.APIKeys().GetByHash(context.Background(), id.HashAPIKey(raw, testPepper))
	if !revoked.Revoked() {
		t.Fatal("key not revoked after revoke")
	}
}

func TestAPIKeyCreatesSandboxScopedKey(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")
	status, data := f.doWith(t, cookie, "POST", "/api/api-keys", map[string]string{
		"merchant_id": "AW9X2K4T7Z",
		"environment": "sandbox",
	})
	if status != 201 {
		t.Fatalf("create: status %d", status)
	}
	raw := data["meta"].(map[string]any)["key"].(string)
	if !strings.HasPrefix(raw, "sk_test_") {
		t.Fatalf("sandbox prefix: %s", raw)
	}
}

// --- providers --------------------------------------------------------------

func TestProviderConfigHotReloadsRegistry(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")

	if _, ok := f.registry.Get("mock.sandbox"); ok {
		t.Fatal("no sandbox mock expected before config save")
	}

	status, data := f.doWith(t, cookie, "PUT", "/api/providers/mock/config", map[string]any{
		"environment": "sandbox",
		"config":      map[string]string{"secret": "s3cret", "va_prefix": "99999"},
	})
	if status != 200 {
		body, _ := anyJSON(data)
		t.Fatalf("save config: status %d body %s", status, body)
	}
	state := data["data"].(map[string]any)
	if state["active"] != true || state["configured"] != true {
		t.Fatalf("state after save: %v", state)
	}
	if p, ok := f.registry.Get("mock.sandbox"); !ok || p.Name() != mockName {
		t.Fatal("mock.sandbox instance not live after save")
	}

	// The saved secret round-trips only into the store, never into output.
	status, listed := f.doWith(t, cookie, "GET", "/api/providers", nil)
	if status != 200 {
		t.Fatalf("list providers: status %d", status)
	}
	raw, _ := anyJSON(listed)
	if strings.Contains(string(raw), "s3cret") {
		t.Fatal("stored secret leaked through the providers endpoint")
	}
}

func TestProviderConfigKeepsSecretWhenBlank(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")

	f.doWith(t, cookie, "PUT", "/api/providers/mock/config", map[string]any{
		"environment": "sandbox",
		"config":      map[string]string{"secret": "first-secret"},
	})
	status, _ := f.doWith(t, cookie, "PUT", "/api/providers/mock/config", map[string]any{
		"environment": "sandbox",
		"config":      map[string]string{"secret": "", "va_prefix": "77777"},
	})
	if status != 200 {
		t.Fatalf("second save: status %d", status)
	}
	stored, err := f.store.ProviderConfigs().Get(context.Background(), "mock", "sandbox")
	if err != nil {
		t.Fatalf("stored row: %v", err)
	}
	if stored.Config["secret"] != "first-secret" {
		t.Fatalf("secret overwritten by blank: %q", stored.Config["secret"])
	}
	if stored.Config["va_prefix"] != "77777" {
		t.Fatalf("non-secret not updated: %q", stored.Config["va_prefix"])
	}
}

func TestProviderConfigRejectsInvalidEnvironment(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")
	status, _ := f.doWith(t, cookie, "PUT", "/api/providers/mock/config", map[string]any{
		"environment": "staging",
		"config":      map[string]string{},
	})
	if status != 400 {
		t.Fatalf("staging: status %d", status)
	}
}

func TestRemoveProviderConfigRetiresInstance(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")

	f.doWith(t, cookie, "PUT", "/api/providers/mock/config", map[string]any{
		"environment": "sandbox",
		"config":      map[string]string{"secret": "gone-soon"},
	})
	status, _ := f.doWith(t, cookie, "DELETE", "/api/providers/mock/config?env=sandbox", nil)
	if status != 200 {
		t.Fatalf("delete: status %d", status)
	}
	if _, ok := f.registry.Get("mock.sandbox"); ok {
		t.Fatal("instance should be retired after credential removal")
	}
}

const mockName = "mock" // mock.Provider#Name constant mirrored for assertions

// --- merchants --------------------------------------------------------------

func TestMerchantLifecycleAndRouting(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "operator@console.test", "operator")

	status, data := f.doWith(t, cookie, "POST", "/api/merchants", map[string]string{
		"name":         "New Merchant",
		"callback_url": "https://new.test/hook",
	})
	if status != 201 {
		t.Fatalf("create merchant: status %d", status)
	}
	m := data["data"].(map[string]any)
	if m["name"] != "New Merchant" {
		t.Fatalf("created merchant: %v", m)
	}
	secret := data["meta"].(map[string]any)["webhook_secret"].(string)
	if !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("webhook secret: %s", secret)
	}
	idLatency := m["id"].(string)

	// Suspend.
	cookie = f.loginAs(t, "owner@console.test", "owner")
	status, data = f.doWith(t, cookie, "PATCH", "/api/merchants/"+idLatency, map[string]string{"status": "suspended"})
	if status != 200 {
		t.Fatalf("suspend: status %d", status)
	}
	if v := data["data"].(map[string]any)["status"]; v != "suspended" {
		t.Fatalf("status after suspend: %v", v)
	}

	// Routing requires live instances.
	status, _ = f.doWith(t, cookie, "PUT", "/api/merchants/"+idLatency+"/routing", domain.RoutingConfig{
		Strategy: domain.StrategyPriority,
		Providers: []domain.RoutingProvider{
			{Name: "nowhere", Enabled: true},
		},
	})
	if status != 400 {
		t.Fatalf("unknown provider routing: status %d", status)
	}
	status, _ = f.doWith(t, cookie, "PUT", "/api/merchants/"+idLatency+"/routing", domain.RoutingConfig{
		Strategy: domain.StrategyPriority,
		Providers: []domain.RoutingProvider{
			{Name: "mock", Enabled: true},
		},
	})
	if status != 200 {
		t.Fatalf("valid routing: status %d", status)
	}

	// Conditional rules are validated before they are stored: an impossible
	// condition would silently never fire.
	status, _ = f.doWith(t, cookie, "PUT", "/api/merchants/"+idLatency+"/routing", domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Rules: []domain.RoutingRule{
			{Condition: domain.RoutingCondition{Field: domain.ConditionFieldAmount, Operator: domain.ConditionOperatorGte, Value: "abc"}, Providers: []string{"mock"}},
		},
	})
	if status != 400 {
		t.Fatalf("non-numeric amount condition: status %d", status)
	}
	status, _ = f.doWith(t, cookie, "PUT", "/api/merchants/"+idLatency+"/routing", domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Rules: []domain.RoutingRule{
			{Condition: domain.RoutingCondition{Field: "metadata:acq", Operator: domain.ConditionOperatorEq, Value: "xendit"}, Providers: []string{"nowhere"}},
		},
	})
	if status != 400 {
		t.Fatalf("unregistered rule provider: status %d", status)
	}
	status, _ = f.doWith(t, cookie, "PUT", "/api/merchants/"+idLatency+"/routing", domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Rules: []domain.RoutingRule{
			{
				Condition: domain.RoutingCondition{
					Field: domain.ConditionFieldAmount, Operator: domain.ConditionOperatorGte, Value: "10000",
				},
				Providers: []string{"mock"},
			},
		},
	})
	if status != 200 {
		t.Fatalf("valid rules: status %d", status)
	}
}

// TestSetupWizardInstallsFirstOwner drives POST /api/setup end to end: the
// first install creates an owner, signs the browser in and locks the endpoint
// afterwards.
func TestSetupWizardInstallsFirstOwner(t *testing.T) {
	f := setup(t)

	// A fresh install reports that it needs setup.
	var statusData struct {
		Data struct {
			NeedsSetup bool `json:"needs_setup"`
		} `json:"data"`
	}
	var resp *http.Response
	let := func(step string, status int, raw []byte, wantStatus int) {
		t.Helper()
		if status != wantStatus {
			t.Fatalf("%s: status %d, want %d (%s)", step, status, wantStatus, raw)
		}
	}

	status, _, raw := f.do(t, "GET", "/api/console-status", nil)
	let("console status", status, raw, 200)
	if err := json.Unmarshal(raw, &statusData); err != nil {
		t.Fatalf("decoding console status: %v (%s)", err, raw)
	}
	if !statusData.Data.NeedsSetup {
		t.Fatal("needs_setup = false, want true on a console without accounts")
	}

	// The wizard's account step: too short a password is refused.
	status, _, raw = f.do(t, "POST", "/api/setup", map[string]string{
		"name": "Owner", "email": "owner@new.test", "password": "short",
	})
	let("short password", status, raw, 400)

	// The real install: account created (owner) and signed in.
	status, resp, raw = f.do(t, "POST", "/api/setup", map[string]string{
		"name": "Dewi Owner", "email": "dewi@new.test", "password": "correct horse battery",
	})
	let("setup", status, raw, 201)
	var created struct {
		Data struct {
			User map[string]any `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decoding setup response: %v", err)
	}
	if created.Data.User["role"] != "owner" || created.Data.User["email"] != "dewi@new.test" {
		t.Fatalf("created user: %v", created.Data.User)
	}

	// The setup response must carry the session cookie: the wizard's next
	// steps run over the ordinary session-guarded endpoints.
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "openpay_admin_session" && c.Value != "" {
			session = c
			break
		}
	}
	if session == nil {
		t.Fatal("setup response carries no session cookie")
	}
	status, _, raw = f.doRaw(t, session, "GET", "/api/me", nil)
	let("me with setup cookie", status, raw, 200)

	// The endpoint closed itself: a second install is a conflict.
	status, _, _ = f.do(t, "POST", "/api/setup", map[string]string{
		"name": "Second", "email": "second@new.test", "password": "another password",
	})
	if status != 409 {
		t.Fatalf("second setup: status %d, want 409 setup_completed", status)
	}

	// Console status flips and ordinary login works with the new credential.
	status, _, raw = f.do(t, "GET", "/api/console-status", nil)
	let("console status after setup", status, raw, 200)
	if err := json.Unmarshal(raw, &statusData); err != nil {
		t.Fatalf("decoding console status: %v", err)
	}
	if statusData.Data.NeedsSetup {
		t.Fatal("needs_setup = true after setup")
	}

	status, _, raw = f.do(t, "POST", "/api/login", map[string]string{
		"email": "dewi@new.test", "password": "correct horse battery",
	})
	let("login after setup", status, raw, 200)
}

// TestSetupWizardConcurrentFirstOwner catches a setup path that authorizes on
// a Count call before creating the owner instead of making that transition
// atomic in the repository.
func TestSetupWizardConcurrentFirstOwner(t *testing.T) {
	f := setup(t)
	start := make(chan struct{})
	type result struct {
		status int
		body   []byte
		err    error
	}
	results := make(chan result, 16)

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, err := json.Marshal(map[string]string{
				"name":     "Owner",
				"email":    fmt.Sprintf("owner-%d@example.test", i),
				"password": "correct horse battery",
			})
			if err != nil {
				results <- result{err: err}
				return
			}
			<-start
			req, err := http.NewRequest(http.MethodPost, f.server.URL+"/api/setup", bytes.NewReader(payload))
			if err != nil {
				results <- result{err: err}
				return
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := f.server.Client().Do(req)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			results <- result{status: resp.StatusCode, body: body, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	var created, completed int
	for result := range results {
		if result.err != nil {
			t.Fatalf("POST /api/setup: %v", result.err)
		}
		switch result.status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			if !strings.Contains(string(result.body), "setup_completed") {
				t.Fatalf("setup conflict body = %s, want setup_completed", result.body)
			}
			completed++
		default:
			t.Fatalf("setup status = %d, want 201 or 409 (%s)", result.status, result.body)
		}
	}
	if created != 1 || completed != 15 {
		t.Fatalf("setup results: %d created, %d completed; want 1 and 15", created, completed)
	}
	count, err := f.store.AdminUsers().Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("Count = %d, want 1", count)
	}
}

// TestMerchantIDsUseAWPrefix pins the public merchant identifier contract:
// new merchants get short AW-prefixed IDs of max 10 characters.
func TestMerchantIDsUseAWPrefix(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "operator@console.test", "operator")

	status, data := f.doWith(t, cookie, "POST", "/api/merchants", map[string]string{
		"name": "Shape Merchant", "callback_url": "https://shape.test/hook",
	})
	if status != 201 {
		t.Fatalf("create merchant: status %d", status)
	}
	created, _ := data["data"].(map[string]any)
	mid, _ := created["id"].(string)
	if !id.ValidMerchantID(mid) {
		t.Fatalf("created merchant id %q, want an AW-prefixed id of at most %d characters", mid, id.MerchantIDLength)
	}
}

func TestMerchantRoutingModifyAndRejectValidation(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "operator@console.test", "operator")

	status, data := f.doWith(t, cookie, "POST", "/api/merchants", map[string]string{
		"name": "Reject Merchant", "callback_url": "https://reject.test/hook",
	})
	if status != 201 {
		t.Fatalf("create merchant: status %d", status)
	}
	id := data["data"].(map[string]any)["id"].(string)

	put := func(cfg domain.RoutingConfig) map[string]any {
		t.Helper()
		status, data := f.doWith(t, cookie, "PUT", "/api/merchants/"+id+"/routing", cfg)
		return map[string]any{"status": status, "data": data}
	}

	// A shape-matching label keeps the failures readable.
	expect := func(step string, res map[string]any, wantStatus int) map[string]any {
		t.Helper()
		if res["status"].(int) != wantStatus {
			t.Fatalf("%s: status %d, want %d (body: %v)", step, res["status"].(int), wantStatus, res["data"])
		}
		return res["data"].(map[string]any)
	}

	// A rejecting rule stores cleanly.
	expect("reject rule", put(domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Rules: []domain.RoutingRule{{
			Condition: domain.RoutingCondition{
				Field: domain.ConditionFieldAmount, Operator: domain.ConditionOperatorGte, Value: "1000000",
			},
			Reject: &domain.RoutingReject{Status: 402, Code: "amount_not_supported", Message: "amount too large"},
		}},
	}), 200)

	// The stored rule carries the reject identity back out.
	status, data = f.doWith(t, cookie, "GET", "/api/merchants/"+id, nil)
	if status != 200 {
		t.Fatalf("get merchant: status %d", status)
	}
	routing := data["data"].(map[string]any)["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("rules = %v, want the rejecting rule", routing["rules"])
	}
	rej, _ := rules[0].(map[string]any)["reject"].(map[string]any)
	if rej == nil || rej["code"] != "amount_not_supported" {
		t.Fatalf("stored rule reject = %v, want amount_not_supported", rules[0])
	}
	if int(rej["status"].(float64)) != 402 {
		t.Errorf("stored reject status = %v, want 402", rej["status"])
	}

	// A rule cannot route and reject at the same time.
	expect("reject+providers", put(domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Rules: []domain.RoutingRule{{
			Condition: domain.RoutingCondition{
				Field: domain.ConditionFieldAmount, Operator: domain.ConditionOperatorGte, Value: "1",
			},
			Providers: []string{"mock"},
			Reject:    &domain.RoutingReject{Status: 402, Code: "confused", Message: "x"},
		}},
	}), 400)

	// The reject identity must stay in the error range.
	expect("reject status 200", put(domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Rules: []domain.RoutingRule{{
			Condition: domain.RoutingCondition{
				Field: domain.ConditionFieldAmount, Operator: domain.ConditionOperatorGte, Value: "1",
			},
			Reject: &domain.RoutingReject{Status: 200, Code: "ok", Message: "x"},
		}},
	}), 400)
	expect("reject missing code", put(domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Rules: []domain.RoutingRule{{
			Condition: domain.RoutingCondition{
				Field: domain.ConditionFieldAmount, Operator: domain.ConditionOperatorGte, Value: "1",
			},
			Reject: &domain.RoutingReject{Status: 402, Code: "  ", Message: "x"},
		}},
	}), 400)

	// Modify operations must be known and complete.
	expect("apply unknown op", put(domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Rules: []domain.RoutingRule{{
			Condition: domain.RoutingCondition{
				Field: domain.ConditionFieldAmount, Operator: domain.ConditionOperatorGte, Value: "1",
			},
			Apply:     []domain.RoutingModify{{Op: "delete_all", Value: "x"}},
			Providers: []string{"mock"},
		}},
	}), 400)
	expect("apply missing key", put(domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Rules: []domain.RoutingRule{{
			Condition: domain.RoutingCondition{
				Field: domain.ConditionFieldAmount, Operator: domain.ConditionOperatorGte, Value: "1",
			},
			Apply:     []domain.RoutingModify{{Op: domain.ModifyOpSetMetadata, Key: "  ", Value: "x"}},
			Providers: []string{"mock"},
		}},
	}), 400)

	// Enrichment and the config-level fallback reject store cleanly.
	enrichAndReject := domain.RoutingConfig{
		Strategy:  domain.StrategyPriority,
		Providers: []domain.RoutingProvider{{Name: "mock", Enabled: true}},
		Enrich: []domain.RoutingModify{
			{Op: domain.ModifyOpSetMetadata, Key: "acquirer", Value: "winpay"},
			{Op: domain.ModifyOpSetDescription, Value: "via openpay"},
		},
		Reject: &domain.RoutingReject{Status: 503, Code: "gateways_down", Message: "all gateways declined"},
	}
	expect("enrich + config reject", put(enrichAndReject), 200)
	// The fallback reject identity round-trips too.
	status, data = f.doWith(t, cookie, "GET", "/api/merchants/"+id, nil)
	if status != 200 {
		t.Fatalf("get merchant after enrich/reject: status %d", status)
	}
	routing = data["data"].(map[string]any)["routing"].(map[string]any)
	if got := routing["reject"].(map[string]any); got["code"] != "gateways_down" {
		t.Errorf("stored config reject = %v, want gateways_down", routing["reject"])
	}
	if len(routing["enrich"].([]any)) != 2 {
		t.Errorf("stored enrich = %v, want two edits", routing["enrich"])
	}

	// Out-of-range fallback reject.
	enrichAndReject.Reject = &domain.RoutingReject{Status: 302, Code: "hop", Message: "x"}
	expect("config reject status 302", put(enrichAndReject), 400)
	// Unknown enrichment op.
	enrichAndReject.Reject = nil
	enrichAndReject.Enrich = []domain.RoutingModify{{Op: "wipe", Value: "x"}}
	expect("enrich unknown op", put(enrichAndReject), 400)
}

// --- users ------------------------------------------------------------------

func TestUserInviteReturnsOneTimePassword(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")

	status, data := f.doWith(t, cookie, "POST", "/api/users", map[string]string{
		"email": "tech2@console.test",
		"name":  "Tech Two",
		"role":  "tech",
	})
	if status != 201 {
		t.Fatalf("invite: status %d", status)
	}
	temporary := data["meta"].(map[string]any)["password"].(string)
	if len(temporary) < 12 {
		t.Fatalf("one-time password too short: %q", temporary)
	}

	// The generated password must actually work.
	status, _, _ = f.do(t, "POST", "/api/login", map[string]string{
		"email": "tech2@console.test", "password": temporary,
	})
	if status != 200 {
		t.Fatalf("login with one-time password: status %d", status)
	}
	_ = data
}

func TestUserSuspendKillsSessions(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")

	status, data := f.doWith(t, cookie, "POST", "/api/users", map[string]string{
		"email": "op@console.test", "role": "operator",
	})
	if status != 201 {
		t.Fatalf("invite: status %d", status)
	}
	temporary := data["meta"].(map[string]any)["password"].(string)
	targetCookie := f.login(t, "op@console.test", temporary)

	// Suspend the account; its sessions must die immediately.
	users, _ := f.store.AdminUsers().List(context.Background())
	var targetID string
	for _, u := range users {
		if u.Email == "op@console.test" {
			targetID = u.ID
		}
	}
	status, _ = f.doWith(t, cookie, "PATCH", "/api/users/"+targetID, map[string]string{"status": "suspended"})
	if status != 200 {
		t.Fatalf("suspend: status %d", status)
	}
	status, _ = f.doWith(t, targetCookie, "GET", "/api/overview", nil)
	if status != 401 {
		t.Fatalf("suspended user still authenticated: status %d", status)
	}
}

func TestSelfSuspensionAndSelfRoleChangeBlocked(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")

	users, _ := f.store.AdminUsers().List(context.Background())
	self := users[0].ID

	status, _ := f.doWith(t, cookie, "PATCH", "/api/users/"+self, map[string]string{"status": "suspended"})
	if status != 400 {
		t.Fatalf("self-suspend: status %d", status)
	}
	status, _ = f.doWith(t, cookie, "PATCH", "/api/users/"+self, map[string]string{"role": "tech"})
	if status != 400 {
		t.Fatalf("self-demotion: status %d", status)
	}
}

// --- inbound ----------------------------------------------------------------

func TestInboundReprocessAppliesCallback(t *testing.T) {
	f := setup(t)
	tx := f.seedTransaction(t, "REPROC-1", domain.EnvironmentProduction)

	event := &domain.InboundEvent{
		ID:             id.NewPrefixed("inb"),
		Provider:       "mock",
		Environment:    tx.Environment,
		EventType:      "payment.paid",
		ReferenceID:    tx.ReferenceID,
		Amount:         tx.Amount,
		Status:         string(provider.StatusPaid),
		SignatureValid: true,
		Parsed:         true,
		ReceivedAt:     time.Now(),
	}
	if err := f.store.Inbound().Create(context.Background(), event); err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	cookie := f.loginAs(t, "operator@console.test", "operator")
	status, data := f.doWith(t, cookie, "POST", "/api/inbound/"+event.ID+"/reprocess", nil)
	if status != 200 {
		body, _ := anyJSON(data)
		t.Fatalf("reprocess: status %d body %s", status, body)
	}
	payload := data["data"].(map[string]any)
	transaction := payload["transaction"].(map[string]any)
	if transaction["status"] != string(provider.StatusPaid) {
		t.Fatalf("reprocessed status: %v", transaction["status"])
	}
	if transaction["environment"] != domain.EnvironmentProduction {
		t.Fatalf("environment after reprocess: %v", transaction["environment"])
	}
}

func TestUnverifiedInboundIsNotReprocessable(t *testing.T) {
	f := setup(t)
	event := &domain.InboundEvent{
		ID:             id.NewPrefixed("inb"),
		Provider:       "mock",
		SignatureValid: false,
		ReceivedAt:     time.Now(),
	}
	if err := f.store.Inbound().Create(context.Background(), event); err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	cookie := f.loginAs(t, "operator@console.test", "operator")
	status, _ := f.doWith(t, cookie, "POST", "/api/inbound/"+event.ID+"/reprocess", nil)
	if status != 422 {
		t.Fatalf("reprocess unsigned: status %d", status)
	}
}

// --- deliveries -------------------------------------------------------------

func TestDeliveryRetryRequeuesFailed(t *testing.T) {
	f := setup(t)
	tx := f.seedTransaction(t, "RETRY-1", domain.EnvironmentSandbox)
	now := time.Now()
	delivery := &domain.WebhookDelivery{
		ID:            id.NewPrefixed("whd"),
		TransactionID: tx.ID,
		MerchantID:    tx.MerchantID,
		Environment:   domain.EnvironmentSandbox,
		EventType:     "payment.paid",
		URL:           "https://example.test/callback",
		Payload:       []byte("{}"),
		Status:        domain.DeliveryFailed,
		Attempts:      5,
		MaxAttempts:   5,
		NextAttemptAt: &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := f.store.Webhooks().Create(context.Background(), delivery); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	cookie := f.loginAs(t, "tech@console.test", "tech")
	status, _ := f.doWith(t, cookie, "POST", "/api/deliveries/"+delivery.ID+"/retry", nil)
	if status != 200 {
		t.Fatalf("retry: status %d", status)
	}
	stored, _ := f.store.Webhooks().GetByID(context.Background(), delivery.ID)
	if stored.Status != domain.DeliveryPending || stored.Attempts != 0 {
		t.Fatalf("delivery after retry: status %s attempts %d", stored.Status, stored.Attempts)
	}
}

// --- audit ------------------------------------------------------------------

func TestAuditLogReadableAndSearched(t *testing.T) {
	f := setup(t)
	cookie := f.loginAs(t, "owner@console.test", "owner")

	// Generate two audited actions with different actors.
	op := f.loginAs(t, "operator@console.test", "operator")
	tx := f.seedTransaction(t, "AUD-1", domain.EnvironmentSandbox)
	f.doWith(t, op, "POST", "/api/transactions/"+tx.ID+"/cancel", nil)
	f.doWith(t, cookie, "POST", "/api/api-keys", map[string]string{
		"merchant_id": "AW9X2K4T7Z", "environment": "sandbox",
	})

	status, data := f.doWith(t, cookie, "GET", "/api/audit?q=cancel", nil)
	if status != 200 {
		t.Fatalf("audit: status %d", status)
	}
	rows := data["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("searched audit rows: %d", len(rows))
	}
	if act := rows[0].(map[string]any)["action"]; act != "transaction.cancel" {
		t.Fatalf("audit row action: %v", act)
	}
}

// --- overview ---------------------------------------------------------------

func TestOverviewFlagsAndCounts(t *testing.T) {
	f := setup(t)
	f.seedTransaction(t, "OVH-1", domain.EnvironmentSandbox)

	cookie := f.loginAs(t, "owner@console.test", "owner")
	status, data := f.doWith(t, cookie, "GET", "/api/overview?env=sandbox", nil)
	if status != 200 {
		t.Fatalf("overview: status %d", status)
	}
	root := data["data"].(map[string]any)
	if root["total_transactions"].(float64) != 1 {
		t.Fatalf("total: %v", root["total_transactions"])
	}
	if len(root["created_per_hour"].([]any)) != 24 {
		t.Fatal("hour buckets not 24")
	}
}

// helpers ---------------------------------------------------------------------

func anyJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
