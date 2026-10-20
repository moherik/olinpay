# Transaction Activity Timeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist and display every transaction boundary activity so operators can locate failures from merchant request through provider response and merchant webhook delivery.

**Architecture:** Add an append-only transaction activity repository with redaction and bounded payloads. Instrument the API handler, shared provider HTTP transport, inbound receiver, and outbound dispatcher into the same activity stream. Expose one transaction-scoped admin endpoint and make the console timeline the primary detail view.

**Tech Stack:** Go, PostgreSQL/pgx, in-memory repositories, chi, Next.js 16, React 19, TypeScript, Tailwind CSS 4.

**Spec:** `docs/superpowers/specs/2026-10-20-transaction-activity-design.md`

## Global Constraints

- Preserve merchant scoping, payment idempotency, callback verification, and outbound webhook retry semantics.
- Redact credentials before persistence; never store authorization, signatures, cookies, API keys, webhook secrets, or private keys.
- Bodies are capped at 64 KiB before persistence.
- Activity persistence failures never change payment, callback, or delivery outcomes.
- Use existing in-memory repositories and `httptest`; live gateway tests remain opt-in.
- The console uses the existing IBM Plex/token palette, full-width shell, dense operations-console direction, and responsive 44px controls.

## Review Focus

- Provider calls before transaction persistence must still appear on the transaction timeline through merchant/environment/reference correlation (Task 3).
- Failed merchant webhook retries must appear individually without changing delivery backoff/status behavior (Task 4).
- Redaction must happen before database writes, including headers and nested sensitive JSON keys (Task 1).
- Activity retrieval must not cross merchant scope or expose unbounded payloads (Task 2).
- Timeline must distinguish empty, loading, error, request, response, callback, retry, and long-payload states on mobile (Task 5).

---

### Task 1: Activity Domain, Repository, Redaction, and Migration

**Files:**
- Create: `internal/domain/activity.go`
- Create: `internal/repository/activity.go`
- Create: `internal/repository/postgres/activity.go`
- Modify: `internal/testutil/memory/memory.go`
- Create: `internal/testutil/memory/activity_test.go`
- Create: `migrations/0004_transaction_activity.sql`
- Create: `internal/platform/activity/redact.go`
- Create: `internal/platform/activity/redact_test.go`

**Interfaces:**
- `repository.TransactionActivityRepository.Create(ctx, *domain.TransactionActivity) error`
- `repository.TransactionActivityRepository.List(ctx, repository.ActivityFilter) ([]*domain.TransactionActivity, error)`
- `activity.RedactHeaders(http.Header) http.Header`
- `activity.RedactBody([]byte) string`

- [ ] **Step 1: Write failing redaction/repository tests**
- [ ] **Step 2: Run focused tests and verify expected failure**
- [ ] **Step 3: Implement the append-only model, 64 KiB cap, redaction, memory repository, PostgreSQL repository, and migration**
- [ ] **Step 4: Verify focused tests and migration SQL shape**
- [ ] **Step 5: Commit `feat(activity): add persisted redacted transaction activity`**

### Task 2: Admin Activity Endpoint and Wiring

**Files:**
- Modify: `internal/admin/server.go`
- Modify: `internal/admin/transactions.go`
- Modify: `internal/admin/admin_test.go`
- Modify: `internal/admin/helpers.go` only if response mapping needs it
- Modify: `cmd/openpay/main.go`
- Modify: `admin/src/lib/api.ts`
- Modify: `admin/src/lib/demo-data.ts` only for types if needed

**Interfaces:**
- `GET /admin/api/transactions/{id}/activities?limit=200`
- `Deps.Activity repository.TransactionActivityRepository`
- `apiGet<RawActivity[]>` mapping with explicit direction/phase/status fields

- [ ] **Step 1: Add failing admin API test for scoped chronological activities**
- [ ] **Step 2: Run the focused admin test and verify failure**
- [ ] **Step 3: Implement route, transaction existence/scope check, bounded limit, and dependency wiring**
- [ ] **Step 4: Add tests for missing transaction, empty activity, and limit cap**
- [ ] **Step 5: Run admin Go tests and commit `feat(admin): expose transaction activity`**

### Task 3: Capture Merchant and Provider Request/Response Activity

**Files:**
- Modify: `pkg/provider/httpclient.go`
- Modify: `pkg/provider/types.go` or new `pkg/provider/trace.go`
- Modify: `internal/core/engine.go`
- Modify: `internal/api/payments.go`
- Modify: `internal/api/server.go` or API deps for the activity repository
- Modify: `cmd/openpay/main.go`
- Modify: `internal/core/engine_test.go`
- Modify: `internal/api/api_test.go`
- Modify: `pkg/provider/httpclient_test.go`

**Interfaces:**
- `provider.HTTPTrace` carries redacted request/response data, status, duration, and error.
- `provider.SetHTTPActivityRecorder(func(context.Context, HTTPTrace))`
- `provider.WithTraceContext(ctx, provider.TraceContext{MerchantID, Environment, ReferenceID, TransactionID}) context.Context`

- [ ] **Step 1: Add failing tests for provider trace capture and merchant create request/response activities**
- [ ] **Step 2: Run focused tests and verify failure**
- [ ] **Step 3: Implement the optional provider recorder and core trace context without making persistence errors affect payment results**
- [ ] **Step 4: Capture redacted merchant API create request/response around successful transaction creation**
- [ ] **Step 5: Verify idempotent replay and provider-failure behavior do not create misleading successful response activities**
- [ ] **Step 6: Run focused/full Go tests and commit `feat(activity): capture merchant and provider exchanges`**

### Task 4: Capture Provider Callbacks and Merchant Webhook Attempts

**Files:**
- Modify: `internal/webhook/receiver.go`
- Modify: `internal/webhook/dispatcher.go`
- Modify: `internal/webhook/dispatcher_test.go`
- Modify: `internal/webhook/receiver_test.go`
- Modify: `cmd/openpay/main.go`

**Interfaces:**
- Receiver and dispatcher receive `repository.TransactionActivityRepository`.
- Provider callback activities retain signature/parse/application outcome and response status/body.
- Merchant webhook activities record every retry attempt with delivery ID, response status, duration, and error.

- [ ] **Step 1: Add failing receiver/dispatcher tests for activity rows**
- [ ] **Step 2: Run focused tests and verify failure**
- [ ] **Step 3: Implement provider callback request/response activity recording while preserving existing inbound rows and HTTP responses**
- [ ] **Step 4: Implement merchant webhook attempt activity recording, including transport errors and non-2xx responses**
- [ ] **Step 5: Verify retry/backoff tests remain unchanged and activity write failures are non-fatal**
- [ ] **Step 6: Run focused/full Go tests and commit `feat(activity): trace callback and webhook delivery attempts`**

### Task 5: Refactor Transaction Detail Into Activity Timeline

**Files:**
- Modify: `admin/src/app/(console)/transactions/[id]/page.tsx`
- Modify: `admin/src/lib/api.ts`
- Create: `admin/src/lib/transaction-activity.ts`
- Create: `admin/src/lib/transaction-activity.test.mjs`
- Modify: `admin/package.json`
- Modify: `DESIGN.md` if the detail-page hierarchy changes need recording

**Interfaces:**
- `RawActivity` and `TransactionActivity` types map the admin endpoint.
- `formatActivityLabel`, `formatActivityOutcome`, and `activityTone` are pure helpers.

- [ ] **Step 1: Add failing pure timeline/state tests**
- [ ] **Step 2: Run focused console tests and verify failure**
- [ ] **Step 3: Replace the tall Actions sidebar with compact header actions and a primary activity timeline**
- [ ] **Step 4: Add expandable redacted headers/body/error sections with long-body containment**
- [ ] **Step 5: Move raw record below activity and preserve cancel/sync/copy behavior**
- [ ] **Step 6: Add loading, empty, error, responsive, keyboard, and light/dark source-level regressions**
- [ ] **Step 7: Run admin typecheck, tests, lint, and build; commit `feat(console): make transaction activity the primary detail`**

### Task 6: Documentation and Whole-Branch Verification

**Files:**
- Modify: `README.md`
- Modify: `docs/PRD.md`
- Modify: `docs/openapi.yaml`

- [ ] **Step 1: Document activity retention, redaction, endpoint, and payload cap**
- [ ] **Step 2: Run full Go and admin verification**
- [ ] **Step 3: Run `go test -race ./...` and migration tests**
- [ ] **Step 4: Inspect mobile/tablet/desktop detail layout and keyboard expansion behavior**
- [ ] **Step 5: Request whole-branch code review before claiming completion**
