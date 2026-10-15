# openpay.id admin dashboard: plan

Status: planned, not built. This document is the source of truth for the admin
dashboard work. Read it with `README.md`, `docs/PRD.md` and `DESIGN.md`. Update it
when scope, architecture or known limitations change.

The admin dashboard is a control plane for owners, operators and the technical
team. It manages providers, users, merchants, API keys, routing, environments,
transactions and webhooks in one console. It is a Next.js app on top of a new
admin API in the existing Go binary.

The README and `docs/PRD.md` already list "an admin API for merchants and routing
configuration (today: `openpay seed`)" as planned. This plan implements that and
the console in front of it. Nothing described here is built yet.

## 1. Scope

In scope:

1. Platform overview with real aggregates.
2. Users and roles (owner, operator, tech).
3. Provider management: catalog, per-provider config requirements, activation.
4. Environments: sandbox and production as real, separate configuration.
5. Merchant management, multi-merchant, each with its own providers and
   environment configuration.
6. Transaction monitoring and operations.
7. Webhook monitoring: inbound and outbound, with retry.
8. Admin audit log.

Out of scope for now, and intentionally not claimed: refunds as a first-class
operation, payouts, settlement and reconciliation, metrics/tracing backends,
message queues. The platform has no refund operation today; the console will not
pretend otherwise.

## 2. Current state vs what the dashboard needs

Verified against the code and `migrations/0001_init.sql`. Working tree was clean.

| Capability | Today | Needed |
| --- | --- | --- |
| Frontend | none; only Swagger UI at `/docs` | Next.js app |
| Admin identity | none; auth is merchant API keys (`internal/api/middleware.go`) | users, roles, sessions |
| Merchant CRUD | `openpay seed` only; `MerchantRepository.Update` exists but is never called | admin CRUD and routing editor |
| Provider credentials | env vars `OPENPAY_PROVIDER_<NAME>_<KEY>`, fixed at process start (`internal/config/config.go`) | stored per scope, per environment |
| Environments | `OPENPAY_ENV` is logged and otherwise unused | first-class dimension in the schema |
| Transactions | merchant-scoped read filters, no count (`internal/api/payments.go`) | cross-merchant list, detail, timeline, actions |
| Inbound webhooks | processed in memory, then discarded (`internal/webhook/receiver.go`) | persisted events |
| Outbound webhooks | persisted queue, automatic backoff only; no list or requeue in `internal/repository/repository.go` | list, manual retry, audit |
| Statistics | none | real SQL aggregates |
| RBAC | none | three roles with an explicit permission map |

Three tables exist: `merchants`, `transactions`, `webhook_deliveries`. Everything
else is new.

## 3. Backend architecture

### 3.1 Admin surface

Add a second HTTP listener in `cmd/openpay` at `OPENPAY_ADMIN_ADDR` (default
`:8082`). HTTP handlers live in `internal/api/admin`; orchestration stays in
`internal/core`; persistence stays behind `internal/repository`. This keeps the
existing package boundaries from `AGENTS.md`.

Why a separate listener: the admin surface has a different auth model and a
different threat model than the merchant payment API. It can be firewalled or
bound to a private network without touching the public API. Merchant key auth is
not modified.

Bootstrap stays in the CLI: `openpay admin create-user`, `openpay admin
reset-password`, `openpay admin list-users`.

### 3.2 Identity, sessions, roles, audit

- Password hashing: argon2id.
- Sessions: DB-backed and revocable. httpOnly, Secure, SameSite=Lax cookie. A CSRF
  token is required on mutations. Login is rate limited.
- Permission map in code, roles assigned to users:

| Permission | owner | operator | tech |
| --- | --- | --- | --- |
| users and roles write | yes | no | no |
| provider credentials write | yes | no | yes |
| merchants and routing write | yes | yes | no |
| transactions read | yes | yes | yes |
| transactions operate (sync, cancel) | yes | yes | no |
| webhooks read | yes | yes | yes |
| webhooks retry | yes | yes | yes |
| audit log read | yes | no | yes |

- Every mutation writes one `admin_audit_logs` row (actor, action, target, diff,
  ip, at). This is not optional for a payment platform.

### 3.3 Environments

An environment (`sandbox`, `production`) is a first-class dimension, not a label.

- `merchant_environments` holds the per-environment configuration: routing,
  callback URL, webhook secret, status. One row per (merchant, environment).
- `transactions` and `webhook_deliveries` gain an `environment` column.
- `api_keys` becomes its own table (see 3.5), keyed by merchant and environment.

Backfilling `environment` on existing rows is a decision, not a default. Guessing
a plausible environment for historical money rows would be a data-integrity claim
with nothing behind it.

### 3.4 Providers: schema and credentials

Two separate problems.

Provider config schema. Each adapter needs different keys. Winpay wants
`partner_id` and `private_key`; Midtrans BI-SNAP wants `client_id`,
`client_secret`, `merchant_id` and `partner_service_id`; DOKU wants
`partner_service_id` and per-bank VA paths. Today this knowledge lives only in the
README prose, so the console cannot build a form or validate input.

Add an optional interface in `pkg/provider`, for example:

```go
type ConfigField struct {
    Key      string
    Label    string
    Type     string // text, password, url, number, select, json
    Required bool
    Secret   bool
    Default  string
    Help     string
    Options  []string
}

type ConfigSchemaProvider interface {
    ConfigSchema() []ConfigField
}
```

Adapters that implement it get a generated form and server-side validation;
adapters that do not fall back to a raw key/value editor. This is additive to the
public contract, so no existing adapter breaks. `pkg/provider/registry.go` already
has `Build`; add a lookup that resolves the schema for a registered name.

Credential storage. `provider_credentials`:

- scope: `platform` or `merchant`;
- merchant_id nullable (null for platform scope);
- environment;
- provider_name;
- config JSONB with secret fields encrypted;
- status, updated_by, updated_at;
- unique on (scope, merchant_id, environment, provider_name).

Two scopes because Indonesian gateways usually work that way: the OpenPay SNAP
partner keys are platform-level, while per-merchant values (a Midtrans QRIS
`sub_merchant_id`, a DOKU VA `merchant_id`) belong on the merchant. Merchant
values override platform defaults.

Secrets: AES-256-GCM under `OPENPAY_SECRETS_KEY`. Write-only in the UI. A read
endpoint returns the mask and last four characters, never the value. The existing
provider tracing already redacts credentials; keep that and never add a second
path that logs raw config. Test-connection action builds a provider from the
stored config and calls a safe read operation.

Provider resolution change. Instances are built once at startup today
(`buildProviders` in `cmd/openpay/main.go`). The core needs a resolver that, given
(merchant, environment, provider), returns a built instance, with a cache keyed by
credential version and invalidation when credentials change. It must fall back to
env config when no DB row exists, must be concurrency-safe, and must handle
multi-instance correctness (a second replica does not see an in-process event, so
use a version column with a short TTL check).

Consequence to handle here: changing credentials currently requires a restart, and
a restart drops the process-local correlation caches in the Winpay, DOKU and
Midtrans BI-SNAP adapters (a documented limitation in `docs/PRD.md`). Once
credentials become dynamically editable, that gap matters more. Persisting
provider correlation metadata with the transaction moves into this work instead of
staying on the roadmap.

### 3.5 Merchant and key management

- Merchant CRUD: create, read, update, suspend. Reuses `MerchantRepository.Update`,
  which is currently dead code.
- `api_keys` table: id, merchant_id, environment, key_hash, key_prefix, label,
  status, last_used_at, created_at, revoked_at. Keys are created once and shown
  once; only the hash and a display prefix are stored, matching the current
  `internal/platform/id` scheme and `OPENPAY_API_KEY_PEPPER`.
- Routing editor backed by `domain.RoutingConfig` (strategy, providers with
  priority and enabled), stored per (merchant, environment).

### 3.6 Observability data

- `provider_events`: transaction_id, merchant_id, environment, provider,
  operation (create, inquiry, cancel), http_status, redacted
  request and response JSONB, duration_ms, at. Powers the transaction timeline,
  provider error rate and latency. The request tracing in `pkg/provider` already
  produces this and currently only logs it.
- `webhook_events_in`: provider, environment, merchant_id (set once correlated),
  received_at, redacted headers, raw_body (bytea, size-capped), signature_valid,
  parse_status, transaction_id, processing_status, error. Retention is
  configurable with a 30-day default, because callback bodies carry payer data.
- Delivery repository gains `List(filter)` and `Requeue(id, actor, reason)`.
  Manual retry sets `status=pending`, `next_attempt_at=now`, keeps the stable
  delivery id (recipients dedupe on it), and writes an audit row.

Inbound reprocessing is not a blind redelivery. Re-applying a callback can
double-apply a transition. Offer view plus a guarded, idempotent re-correlation,
or leave reprocessing out of the first release.

### 3.7 Overview metrics

All from SQL, no invented numbers:

- transaction count by status over a window, and gross amount;
- per-provider success and error rate;
- webhook delivery success rate, queue depth, oldest pending delivery;
- provider latency p50 and p95 from `provider_events`.

A delta is shown only when the comparison window is real and named. No comparison,
no delta.

## 4. Data model

Migration `0002_admin.sql` adds:

- `admin_users`, `admin_sessions`, `admin_audit_logs`;
- `merchant_environments`;
- `api_keys`;
- `provider_credentials`;
- `provider_events`;
- `webhook_events_in`;
- `environment` columns on `transactions` and `webhook_deliveries`.

Sketch (final SQL is written during Phase 0, with indexes and constraints):

```sql
CREATE TABLE admin_users (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  name          TEXT NOT NULL,
  role          TEXT NOT NULL,             -- owner | operator | tech
  status        TEXT NOT NULL DEFAULT 'active',
  totp_secret   TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at TIMESTAMPTZ
);

CREATE TABLE merchant_environments (
  merchant_id       TEXT NOT NULL REFERENCES merchants(id),
  environment       TEXT NOT NULL,         -- sandbox | production
  routing           JSONB NOT NULL DEFAULT '{}',
  callback_url      TEXT,
  webhook_secret_enc BYTEA,
  status            TEXT NOT NULL DEFAULT 'active',
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (merchant_id, environment)
);

CREATE TABLE api_keys (
  id          TEXT PRIMARY KEY,
  merchant_id TEXT NOT NULL REFERENCES merchants(id),
  environment TEXT NOT NULL,
  key_hash    TEXT NOT NULL UNIQUE,
  key_prefix  TEXT NOT NULL,
  label       TEXT,
  status      TEXT NOT NULL DEFAULT 'active',
  last_used_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at  TIMESTAMPTZ
);

CREATE TABLE provider_credentials (
  id            TEXT PRIMARY KEY,
  scope         TEXT NOT NULL,             -- platform | merchant
  merchant_id   TEXT REFERENCES merchants(id),
  environment   TEXT NOT NULL,
  provider_name TEXT NOT NULL,
  config        JSONB NOT NULL,            -- secret fields encrypted
  status        TEXT NOT NULL DEFAULT 'active',
  updated_by    TEXT REFERENCES admin_users(id),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (scope, merchant_id, environment, provider_name)
);
```

Compatibility for existing data: migrate `merchants.api_key_hash` and
`api_key_prefix` into one `api_keys` row each so existing merchants keep working.
The environment for those rows comes from `OPENPAY_ENV` at migration time and is
recorded, not guessed.

Schema changes are forward-only. For each table: domain type, repository
interface, PostgreSQL implementation, in-memory implementation for tests.

## 5. Admin API

Served on the admin listener under `/admin/v1`. Spec in `docs/openapi-admin.yaml`.

| Group | Endpoints |
| --- | --- |
| Auth | `POST /auth/login`, `POST /auth/logout`, `GET /me` |
| Users | `GET /users`, `POST /users`, `PATCH /users/{id}` |
| Merchants | `GET /merchants`, `POST /merchants`, `GET /merchants/{id}`, `PATCH /merchants/{id}` |
| Environments | `GET /merchants/{id}/environments/{env}`, `PUT` routing, `PATCH` callback and status |
| Providers | `GET /providers` (catalog, schema, health), `GET /providers/{name}/schema` |
| Credentials | `GET /provider-credentials`, `PUT`, `POST /provider-credentials/test` |
| API keys | `GET /api-keys`, `POST /api-keys`, `POST /api-keys/{id}/revoke` |
| Transactions | `GET /transactions`, `GET /transactions/count`, `GET /transactions/{id}`, `POST /transactions/{id}/sync`, `POST /transactions/{id}/cancel` |
| Webhooks in | `GET /webhook-events`, `GET /webhook-events/{id}`, `POST /webhook-events/{id}/reprocess` (guarded) |
| Webhooks out | `GET /webhook-deliveries`, `GET /webhook-deliveries/{id}`, `POST /webhook-deliveries/{id}/retry` |
| Metrics | `GET /metrics/overview`, `GET /metrics/providers`, `GET /metrics/webhooks` |
| Audit | `GET /audit-logs` |

Transaction filters: merchant, environment, status (repeated), provider, method,
channel, currency, amount range, reference, date range, plus limit and offset.
The existing filters in `internal/repository/repository.go` gain `Environment`,
`Channel`, `Currency`, `AmountMin`, `AmountMax`, and a count query.

## 6. Frontend architecture

- Next.js App Router in `admin/` in this repository. The Go module stays at the
  root; `go build ./...` ignores the frontend directory. A separate repository is
  an option, at the cost of splitting contract changes across two PRs.
- shadcn/ui and Tailwind, using the tokens in `DESIGN.md` from day one.
- TanStack Query for client data, TanStack Table for dense lists,
  react-hook-form with zod for forms.
- Types generated from `docs/openapi-admin.yaml` with `openapi-typescript`, so the
  contract has one source.
- The browser talks only to Next. A Next `rewrites()` rule proxies `/admin/v1/*`
  to the Go admin port, so the session cookie is same-origin. No CORS, and the Go
  admin listener can stay on a private network.

## 7. Feature map

| Feature | Data source | Screens | Required states |
| --- | --- | --- | --- |
| Overview | SQL aggregates over transactions, deliveries, provider_events | one screen, each chart carries its question | no traffic yet, loading, error |
| Users and roles | admin_users | list, create, edit role | empty, loading, error |
| Providers | registry and schema, provider_credentials | catalog, per-provider config, test connection | unconfigured, configured, health failing |
| Environments | merchant_environments | environment switch on merchant detail, clear current-env marker | sandbox/production distinct |
| Merchants | merchants, merchant_environments, routing, api_keys | list, detail with tabs (environments, providers, routing, keys, transactions, webhooks) | no keys yet, suspended |
| Transactions | transactions, provider_events | cross-merchant list, detail with timeline and actions | no results, in flight, failed, syncing |
| Webhooks | webhook_events_in, webhook_deliveries | inbound list and detail, outbound list and retry | queue empty, all failing, retrying |

Every data screen ships an empty state that names the cause and the next action, a
loading state that says what is loading, and an error state that says what failed.

## 8. Security and correctness requirements

These come from the platform rules in `AGENTS.md` and the existing security notes
in `docs/PRD.md`. They are requirements, not suggestions.

- Secrets are encrypted at rest and never returned by a read endpoint or written
  to a log. Preserve the redaction in `pkg/provider` and do not add a bypass.
- Admin auth is separate from merchant API-key auth. A merchant key must never open
  an admin route, and an admin session must never act as a merchant.
- Every admin mutation is audited with actor, target and diff.
- Money stays integer. The console must not introduce float amounts.
- Manual webhook retry does not change the delivery id, so a recipient can keep
  deduplicating.
- Inbound reprocessing is idempotent or absent. Duplicate callbacks must not
  double-apply effects.
- Retention on `webhook_events_in` and `provider_events` is enforced, not
  documented and forgotten.
- Multi-instance: credential cache invalidation, session lookup and retry must
  behave the same on two replicas as on one.

## 9. Design

UI direction is in `DESIGN.md`: dense operations console, dial ENERGY 1 /
RHYTHM 2 / MOTION 1, measured AA contrast, IBM Plex, border-first hierarchy. The
antislop Delivery Gate runs before any UI is handed over, with the click-through
recorded. Because the console is dense by nature, the two risks to watch are the
default dashboard shell (sidebar, top bar, four stat cards, a chart, a table) and
charts that answer no question. Both are called out in `DESIGN.md`.

### 9.1 The clickable demo build

A walkable demo lives in `admin/`. It was built before any backend work so the
design direction and the screen inventory could be judged against a real
interface rather than a description. It is a static Next.js app. Sample data sits
in `admin/src/lib/demo-data.ts` and it makes no HTTP calls to the platform.

```
cd admin
npm install
npm run dev
```

Screens it covers: the shell and navigation, the environment switch scoping
transactions and webhooks, the transaction list with filters and the detail view
with provider events, raw payload and a cancel that updates shared state, outbound
delivery retry and inbound callback reprocess, merchant creation, per environment
provider configuration, routing order editing, API key create and revoke, the
provider catalog with a field schema per adapter, users and roles, and the audit
log.

Both themes are built. On a first visit with no stored choice the console follows
the operating system, the reader can choose Light, Dark or Match system in the
account menu, and the choice is stored per browser. Every colour is a token, so the
switch is a variable change and no component knows which theme is active.

It is not a prototype of the backend. There is no authentication, no RBAC
enforcement, no persistence past the browser session, and the credential
`Validate fields` button only checks the fields locally. The in app notice and the
account menu say this to anyone using it.

Verified before handover: `npm run lint` and `npm run build` pass, every route and
every interactive control was exercised in a browser at a desktop viewport with no
console errors, the narrow layout was confirmed to switch to the mobile navigation
dialog, the palette meets AA contrast at the measured ratios in `DESIGN.md` in both
themes, and the theme switch was confirmed to set the persisted value, the
document class and the computed colours and to survive a reload with no flash.

This tree is the starting point for Phase 4, not a throwaway. Phase 4 replaces the
sample data with generated API types and a BFF proxy, and adds session handling.

## 10. Phases

Each phase ends with `gofmt`, `go build ./...`, `go vet ./...` and relevant tests.
Cross-cutting phases also run `go test ./...` and `go test -race ./...`.

Phase 0, foundations.

- Migration 0002 for identity, sessions, audit, api_keys,
  merchant_environments, environment columns.
- Admin listener, login, sessions, RBAC, audit writes.
- CLI user bootstrap and password reset.
- Tests with in-memory repos and `httptest`.

Phase 1, merchant control.

- Merchant CRUD, environment switch, routing editor, API key create and revoke.
- Migrate existing merchant keys into `api_keys`.

Phase 2, providers.

- `ConfigSchemaProvider` in `pkg/provider` with adapter schemas.
- `provider_credentials` with encryption and both scopes.
- Resolver, cache, env fallback, test connection.
- Persist provider correlation metadata (Winpay, DOKU, Midtrans BI-SNAP).

Phase 3, observability.

- `provider_events`, `webhook_events_in`.
- Delivery list and requeue with audit.
- Aggregate metrics endpoints, transaction count and richer filters.

Phase 4, frontend.

- Scaffold in `admin/`, tokens from `DESIGN.md`.
- Pages in order: overview, transactions, webhooks, merchants, providers, users.
- Generated API types, BFF proxy, session handling.

Phase 5, hardening.

- TOTP two-factor, retention jobs, multi-instance cache invalidation, rate limits,
  backup and restore notes.
- Update `README.md`, `docs/PRD.md` and this document.

## 11. Risks and compatibility

1. Existing merchant keys must keep working through the `api_keys` migration. The
   backfill environment is a recorded decision.
2. The provider resolver sits on the payment hot path. A mistake there is a payment
   outage. Env fallback, cache TTL and race tests are required, not optional.
3. Losing `OPENPAY_SECRETS_KEY` means every stored credential must be re-entered.
   Document rotation and backup.
4. Persisting inbound callback bodies raises the exposure of payer data. Redaction,
   size caps, retention and restricted access are required together.
5. Dynamic provider config against process-local correlation caches. Handle in
   Phase 2, not later.
6. The known tradeoff in `docs/PRD.md` (gateway success with a local persistence
   failure) is unaffected by the dashboard and must not be presented as fixed.

## 12. Reference: Hyperswitch

Checked at `https://docs.hyperswitch.io/` for feature reference. It is inspiration
for the shape of the control plane, not a template to copy; openpay.id is a
smaller system and must keep its own structure.

- Control center as one interface for payment operations across teams
  (customer experience, finance, growth), with real-time analytics, reports,
  alerts and user roles and access.
- Multi-tenancy hierarchy: a tenant contains organizations, which contain merchant
  accounts and business profiles, with per-tenant branding and sandbox
  environments. The openpay.id equivalent is a flatter
  merchant plus environment model; a tenant layer is out of scope until asked for.
- Smart routing: volume based, rule based, and a default fallback priority order,
  with an eligibility check and fallback when a connector cannot serve a payment.
  openpay.id has `priority` and `round_robin` today; volume and rule based
  strategies are a later addition, and the console should not promise them yet.
- Payment analytics concepts worth reusing in the overview definitions: success
  rate and processed amount, defined precisely so the console number matches the
  ledger query behind it.
- Connector integration as a dashboard flow with credentials per connector, which
  is the model behind section 3.4.

## 13. Decisions to confirm

1. Frontend lives in `admin/` in this repository (recommended) or a separate one.
2. Admin API on its own listener `:8082` (recommended) or a route group on `:8080`.
3. Credentials at both platform and merchant scope (recommended) or merchant only.
4. Backfill value for the environment column on existing rows.

Dark mode is decided: both themes ship from the first frontend release.

## 14. Files and docs to update when work starts

- `migrations/0002_admin.sql` (new).
- `internal/api/admin/*`, `internal/core/*`, `internal/repository/*`,
  `internal/repository/postgres/*`, `internal/testutil/memory/*`.
- `pkg/provider/registry.go` and each adapter for `ConfigSchemaProvider`.
- `internal/config/config.go` for `OPENPAY_ADMIN_ADDR` and
  `OPENPAY_SECRETS_KEY`.
- `cmd/openpay/main.go` for the admin listener and the `admin` subcommands.
- `docs/openapi-admin.yaml` (new), `.env.example`, `README.md`, `docs/PRD.md`.
