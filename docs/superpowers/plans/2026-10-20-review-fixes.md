# Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make browser-only onboarding race-safe and failure-safe, enforce the AW merchant-ID contract, reject cyclic routing graphs, and harden the local development and mobile console experience.

**Architecture:** Move first-owner exclusivity into the repository transaction boundary, model setup's merchant/key sequence explicitly in the console, and keep an empty provider registry as a valid administrative state. Add validation at both Go persistence and PostgreSQL schema boundaries; isolate process supervision in a Linux shell script and mobile navigation in the existing shell component.

**Tech Stack:** Go 1.24, chi, pgx/PostgreSQL, Next.js 16, React 19, TypeScript, Tailwind CSS 4, Radix UI, Bash/Linux.

**Spec:** `docs/superpowers/specs/2026-10-20-review-fixes-design.md`

## Global Constraints

- Browser setup is the only first-administrator bootstrap path; remove `create-admin`.
- No compatibility or data rewrite for historical `mch_...` merchant IDs.
- Merchant IDs are exactly `AW` plus eight characters from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`.
- One-time secrets stay in React memory; never log them or persist them in browser storage.
- Preserve merchant scoping, payment idempotency, callback verification, and outbound webhook guarantees.
- Preserve technical names: Go module path, `OPENPAY_*`, `X-OpenPay-*`, database names, and API paths.
- Local process supervision targets Linux.
- Respect current HEAD changes in `Makefile`, console layout, overview, and app shell; do not restore older layout code.

## Review Focus

- Simultaneous setup requests with different emails: exactly one owner must exist and all losers must receive `409 setup_completed` (Task 1 test).
- API-key request with an ambiguous/lost response: wizard must not retry automatically or recreate the merchant; continue without a key and direct the owner to API Keys (Task 2 test).
- No providers configured: admin/setup must boot while payment routing fails deterministically without panic (Task 3 tests).
- Reachable cycles crossing condition branches: graph validation must report the loop instead of truncating output (Task 5 tests).
- Unhealthy process already owns a dev port: `make dev` must fail without starting a duplicate or overwriting the original diagnosis (Task 6 test).

---

### Task 1: Atomic Browser-Only First Owner

**Files:**
- Modify: `internal/repository/admin.go`
- Modify: `internal/repository/postgres/adminusers.go`
- Modify: `internal/testutil/memory/memory.go`
- Modify: `internal/admin/setup.go`
- Modify: `internal/admin/auth.go`
- Modify: `internal/admin/admin_test.go`
- Modify: `cmd/openpay/main.go`
- Modify: `cmd/openpay/main_test.go`

**Interfaces:**
- Produces: `AdminUserRepository.CreateFirst(context.Context, *domain.AdminUser) error`; returns `domain.ErrConflict` when any console user already exists.
- Preserves: `AdminUserRepository.Create` for authenticated Users API account creation.

- [ ] **Step 1: Add failing repository and HTTP concurrency tests**

Add `TestAdminUserCreateFirstConcurrent` to the memory repository tests (create `internal/testutil/memory/memory_test.go` if needed): launch 16 goroutines with distinct owner IDs/emails behind one start channel, assert one nil result, 15 `domain.ErrConflict`, and `Count()==1`.

Add `TestSetupWizardConcurrentFirstOwner` in `internal/admin/admin_test.go`: issue concurrent `POST /api/setup` requests with distinct valid emails, assert one `201`, every other response is `409` with code `setup_completed`, and the fixture repository contains one user.

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/testutil/memory ./internal/admin -run 'TestAdminUserCreateFirstConcurrent|TestSetupWizardConcurrentFirstOwner' -count=1`

Expected: FAIL because `CreateFirst` does not exist and the current count/create sequence is raceable.

- [ ] **Step 3: Implement atomic `CreateFirst`**

Add the interface method. In memory, hold `Store.mu` across zero-user check and insert. In PostgreSQL, begin a transaction, execute `SELECT pg_advisory_xact_lock($1)` with a private fixed `int64` key, query `SELECT count(*) FROM admin_users`, return wrapped `domain.ErrConflict` when nonzero, insert the supplied row through the transaction, then commit. Roll back on every non-commit exit.

Change `setup` to call `CreateFirst` without a preceding authoritative `Count`. Map `errors.Is(err, domain.ErrConflict)` to the existing `409 setup_completed` response; log other repository failures with their underlying error before returning the generic 500 response.

- [ ] **Step 4: Remove the CLI bootstrap path**

Delete the `create-admin` dispatch case, help line, implementation, imports used only by it, and CLI tests. Change the startup warning to direct operators to `/setup`. Remove `openpay create-admin` wording from setup comments and UI copy; later users remain handled by authenticated `/api/users`.

- [ ] **Step 5: Run focused and package tests**

Run: `gofmt -w internal/repository/admin.go internal/repository/postgres/adminusers.go internal/testutil/memory/memory.go internal/testutil/memory/memory_test.go internal/admin/setup.go internal/admin/auth.go internal/admin/admin_test.go cmd/openpay/main.go cmd/openpay/main_test.go && go test ./internal/testutil/memory ./internal/admin ./cmd/openpay -count=1`

Expected: PASS.

- [ ] **Step 6: Add an opt-in PostgreSQL concurrency test**

Create `internal/repository/postgres/adminusers_test.go` with `TestAdminUserCreateFirstConcurrentPostgres`. Read `OPENPAY_TEST_DATABASE_URL`; skip when absent. Run migrations or require a migrated disposable DB, truncate `admin_sessions` then `admin_users`, race 16 `CreateFirst` calls, and assert one row. Never point this test at the normal development database.

Run when configured: `OPENPAY_TEST_DATABASE_URL=... go test ./internal/repository/postgres -run TestAdminUserCreateFirstConcurrentPostgres -count=1`

- [ ] **Step 7: Commit**

```bash
git add internal/repository/admin.go internal/repository/postgres/adminusers.go internal/repository/postgres/adminusers_test.go internal/testutil/memory internal/admin cmd/openpay
git commit -m "fix(setup): create the first owner atomically"
```

### Task 2: Failure-Safe Merchant and API-Key Setup

**Files:**
- Create: `admin/src/lib/setup-state.mjs`
- Create: `admin/src/lib/setup-state.test.mjs`
- Modify: `admin/package.json`
- Modify: `admin/src/app/setup/page.tsx`

**Interfaces:**
- Produces: pure state transitions from `setup-state.mjs`: `merchantCreated(state, payload)`, `keyFailed(state, message)`, `keyCreated(state, key)`, and `continueWithoutKey(state)`.
- Merchant payload: `{ id: string, name: string, environment: string, webhookSecret: string | null }`.
- Final state distinguishes `complete-with-key` from `complete-without-key`.

- [ ] **Step 1: Write failing state-machine tests**

In `setup-state.test.mjs`, use `node:test` and `node:assert/strict`. Assert:

- `initial → merchant-created → key-failed → complete-without-key` retains merchant ID and webhook secret.
- A `key-failed` transition cannot return to merchant creation.
- `initial → merchant-created → key-created` retains both secrets.
- Back/reopen does not clear partial merchant state.
- No transition automatically retries or revokes a key.

Add script: `"test:setup": "node --test src/lib/setup-state.test.mjs"`.

- [ ] **Step 2: Run tests and confirm failure**

Run: `npm --prefix admin run test:setup`

Expected: FAIL because the state module does not exist.

- [ ] **Step 3: Implement the pure state module**

Use frozen/plain object returns; reject invalid transitions with `TypeError`. Keep it framework-free and secret-storage-free.

- [ ] **Step 4: Integrate state into `MerchantForm`**

After merchant creation, store merchant ID and webhook secret before calling `/api-keys`. On key failure, render the webhook secret, explain that the owner must inspect API Keys after setup, and provide `Continue without a key`; do not show merchant creation fields or call `/api-keys` again. On success, show both secrets as today.

Register `beforeunload` only while a webhook secret or API key is visible and the step is not acknowledged; remove it on acknowledgement/unmount. Preserve Back and card reopen state for the current page lifetime.

- [ ] **Step 5: Run setup tests, typecheck, and lint**

Run: `npm --prefix admin run test:setup && (cd admin && npx tsc --noEmit) && npm --prefix admin run lint`

Expected: PASS with no warnings.

- [ ] **Step 6: Commit**

```bash
git add admin/package.json admin/src/lib/setup-state.mjs admin/src/lib/setup-state.test.mjs admin/src/app/setup/page.tsx
git commit -m "fix(setup): preserve merchant secrets after key failure"
```

### Task 3: Administrative Boot With No Providers

**Files:**
- Modify: `cmd/openpay/main.go`
- Modify: `cmd/openpay/main_test.go`
- Test: `internal/core/routing_test.go`

**Interfaces:**
- Produces: `buildRegistry(...) (*provider.Registry, error)` may return an empty non-nil registry with nil error.
- Preserves: invalid explicitly configured environment providers still fail startup.

- [ ] **Step 1: Change the empty-registry test first**

Replace `TestBuildRegistryEmpty` assertions: expect nil error, non-nil registry, and `Len()==0`. Add/extend a core test proving create-payment with an empty registry returns the existing no-provider/unroutable domain error and does not panic.

- [ ] **Step 2: Run focused tests and confirm failure**

Run: `go test ./cmd/openpay ./internal/core -run 'TestBuildRegistryEmpty|Test.*NoProvider' -count=1`

Expected: `TestBuildRegistryEmpty` fails because startup returns an error.

- [ ] **Step 3: Permit the empty administrative state**

Remove the terminal error from `buildRegistry`; log one warning that payment routing is unavailable until a provider is configured. Keep eager failures for explicitly configured invalid providers and failures loading individual DB provider rows.

- [ ] **Step 4: Verify and commit**

Run: `gofmt -w cmd/openpay/main.go cmd/openpay/main_test.go internal/core/routing_test.go && go test ./cmd/openpay ./internal/core -count=1`

```bash
git add cmd/openpay/main.go cmd/openpay/main_test.go internal/core/routing_test.go
git commit -m "fix(startup): allow onboarding without providers"
```

### Task 4: Enforce AW Merchant IDs

**Files:**
- Modify: `internal/platform/id/id.go`
- Modify: `internal/platform/id/id_test.go`
- Modify: `internal/repository/postgres/merchant.go`
- Modify: `internal/testutil/memory/memory.go`
- Create: `migrations/0003_merchant_id.sql`
- Test: `internal/testutil/memory/memory_test.go`

**Interfaces:**
- Produces: `id.ValidMerchantID(string) bool` accepting exactly `^AW[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{8}$`.
- Persistence `Create` returns an error wrapping `domain.ErrValidation` for invalid merchant IDs.

- [ ] **Step 1: Write failing validation tests**

Rename shape tests around `ValidMerchantID`. Table-test valid `AW9X2K4T7Z` and invalid prefix, lowercase, forbidden `0/1/I/O`, 9/11-character lengths, and historical `mch_...`. Add an in-memory repository test asserting invalid create returns `domain.ErrValidation` and stores nothing.

- [ ] **Step 2: Run tests and confirm failure**

Run: `go test ./internal/platform/id ./internal/testutil/memory -run 'MerchantID|InvalidMerchant' -count=1`

- [ ] **Step 3: Add application and database enforcement**

Implement `ValidMerchantID`; call it before PostgreSQL/in-memory inserts. Add `0003_merchant_id.sql` with a named `CHECK` using `id ~ '^AW[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{8}$'`. Do not transform existing rows.

- [ ] **Step 4: Verify migration behavior**

On a disposable migrated database, assert a valid insert succeeds and an invalid ID fails with the named constraint. Document that incompatible rows cause migration failure and require reset/cleanup.

- [ ] **Step 5: Run tests and commit**

Run: `gofmt -w internal/platform/id/id.go internal/platform/id/id_test.go internal/repository/postgres/merchant.go internal/testutil/memory/memory.go internal/testutil/memory/memory_test.go && go test ./internal/platform/id ./internal/testutil/memory ./internal/admin ./cmd/openpay -count=1`

```bash
git add internal/platform/id internal/repository/postgres/merchant.go internal/testutil/memory migrations/0003_merchant_id.sql
git commit -m "fix(merchants): enforce AW identifiers"
```

### Task 5: Reject Cyclic Routing Graphs

**Files:**
- Create: `admin/src/lib/routing-cycle.mjs`
- Create: `admin/src/lib/routing-cycle.test.mjs`
- Modify: `admin/package.json`
- Modify: `admin/src/lib/routing-graph.ts`

**Interfaces:**
- Produces: `cycleNodeIDs(nodes, edges, entryID) => string[]`, a deterministic deduplicated list of reachable cycle target IDs.
- `validateGraph` converts each ID into issue text `Routing flow contains a cycle — remove the loop.` before compilation.

- [ ] **Step 1: Write failing directed-graph tests**

Use `node:test` with plain `{id}` nodes and `{source,target}` edges. Cover fallback cycle, match cycle, no-match cycle, cross-branch cycle, unreachable cycle (not reported), and acyclic shared tail (not reported).

- [ ] **Step 2: Run tests and confirm failure**

Add `"test:routing": "node --test src/lib/routing-cycle.test.mjs"` and run `npm --prefix admin run test:routing`.

- [ ] **Step 3: Implement DFS and integrate before compilation**

Use white/gray/black DFS reachable from entry; record targets of edges to gray nodes, deduplicate, and sort by original node order. Keep existing compiler `visited` guards solely for termination.

- [ ] **Step 4: Verify and commit**

Run: `npm --prefix admin run test:routing && (cd admin && npx tsc --noEmit) && npm --prefix admin run lint`

```bash
git add admin/package.json admin/src/lib/routing-cycle.mjs admin/src/lib/routing-cycle.test.mjs admin/src/lib/routing-graph.ts
git commit -m "fix(routing): reject cyclic editor graphs"
```

### Task 6: Supervise Local Development Processes

**Files:**
- Create: `scripts/dev.sh`
- Create: `scripts/dev_test.sh`
- Modify: `Makefile`
- Modify: `.gitignore`

**Interfaces:**
- `scripts/dev.sh start` starts/reuses both services; `scripts/dev.sh stop` stops owned process groups.
- Environment overrides: `AWSEMPAY_DEV_STATE_DIR`, `AWSEMPAY_API_COMMAND`, `AWSEMPAY_DASHBOARD_COMMAND`, `AWSEMPAY_API_URL`, `AWSEMPAY_DASHBOARD_URL`, `AWSEMPAY_API_PORT`, `AWSEMPAY_DASHBOARD_PORT`, `AWSEMPAY_DEV_TIMEOUT`.
- `make dev` invokes `start`; `make dev-stop` invokes `stop`.

- [ ] **Step 1: Write failing shell scenarios**

In `scripts/dev_test.sh`, create a temporary state directory and fake service commands. Assert healthy reuse; occupied/unhealthy port exits nonzero without launching; delayed readiness succeeds; early child exit fails with log path; timeout fails; stale PID is replaced; stop terminates the recorded process group but not an unrelated process.

- [ ] **Step 2: Run tests and confirm failure**

Run: `bash scripts/dev_test.sh`

Expected: FAIL because `scripts/dev.sh` does not exist.

- [ ] **Step 3: Implement Linux process supervision**

Use `setsid` for owned process groups, `kill -0` plus `/proc` command verification for PID reuse, `ss`/`fuser` only to diagnose occupied ports, bounded `curl` polling, and TERM to negative PGID on stop. Store logs/PIDs under `${AWSEMPAY_DEV_STATE_DIR:-/tmp/awsempay-dev}`. Never kill an unrecorded port owner.

- [ ] **Step 4: Wire Make targets and reset flow**

Replace the recipe body with `bash scripts/dev.sh start`; add `dev-stop`. Keep the current destructive reset behavior and make it propagate dev startup failure. Ignore `.superpowers/` and repository-local transient process-state paths, preserving unrelated ignore entries.

- [ ] **Step 5: Verify and commit**

Run: `bash scripts/dev_test.sh && make -n dev && make -n dev-stop`

```bash
git add scripts/dev.sh scripts/dev_test.sh Makefile .gitignore
git commit -m "fix(dev): supervise local API and dashboard processes"
```

### Task 7: Mobile Shell, Branding, and Documentation

**Files:**
- Modify: `admin/src/components/shell/app-shell.tsx`
- Modify: `admin/src/app/setup/page.tsx`
- Modify: `README.md`
- Modify: `admin/README.md`
- Modify: `docs/PRD.md`
- Modify: `docs/openapi.yaml`
- Modify: `DESIGN.md`

**Interfaces:**
- Desktop horizontal nav remains at the current content-fit breakpoint.
- Mobile exposes a labeled `Menu` control and every `navItems` destination in an accessible dialog/dropdown with `aria-current` on the active destination.
- Mobile interactive rows/buttons are at least 44px; desktop retains dense controls.

- [ ] **Step 1: Implement mobile navigation without reverting current shell work**

Reuse `navItems`, `isActive`, and existing Radix dialog/dropdown primitives. Hide horizontal nav when it no longer fits; show `Menu` at that breakpoint. Ensure destination rows, environment buttons, and account trigger are 44px on mobile and return to dense dimensions on desktop. Preserve current full-width shell, current environment-switch placement, current overview containment, amber sandbox wash, and production wash.

- [ ] **Step 2: Update visible product copy and contracts**

Remove every `create-admin` instruction. Change visible OpenAPI title/description/contact labels to AwsemPay while retaining technical identifiers. Replace `AW239823` with `AW9X2K4T7Z`. Document browser-only setup, empty-provider startup, strict migration behavior, partial merchant/key failure, and `make dev-stop`. Update `DESIGN.md` product display name and mobile-shell behavior.

- [ ] **Step 3: Run static verification**

Run: `(cd admin && npx tsc --noEmit) && npm --prefix admin run lint && npm --prefix admin run build`

Expected: all pass without warnings.

- [ ] **Step 4: Perform focused accessibility/layout checks**

At narrow, tablet, and desktop widths, verify keyboard opening/closing, focus return, active-page announcement, 44px targets, no horizontal page overflow, compact sticky height, and named environment state in light/dark themes. Record findings in the commit body or final report; do not claim checks not performed.

- [ ] **Step 5: Commit**

```bash
git add admin/src/components/shell/app-shell.tsx admin/src/app/setup/page.tsx README.md admin/README.md docs/PRD.md docs/openapi.yaml DESIGN.md
git commit -m "fix(console): complete onboarding and mobile shell contracts"
```

### Task 8: Whole-Branch Verification and Review

**Files:**
- Modify only files required by verified defects.

**Interfaces:**
- Consumes all prior task interfaces; produces a release-ready reviewed branch.

- [ ] **Step 1: Run complete verification**

```bash
gofmt -w $(git diff --name-only --diff-filter=ACM HEAD~7 -- '*.go')
go build ./...
go vet ./...
go test ./...
go test -race ./...
npm --prefix admin run test:setup
npm --prefix admin run test:routing
npm --prefix admin run lint
npm --prefix admin run build
bash scripts/dev_test.sh
git status --short
```

Expected: every command passes; status contains only intentional plan/spec changes not yet committed.

- [ ] **Step 2: Run the opt-in PostgreSQL tests on a disposable database when available**

Run migration through `0003`, first-owner concurrency, and valid/invalid merchant insert checks. Report explicitly if unavailable.

- [ ] **Step 3: Request whole-range code review**

Use `superpowers:requesting-code-review` from the commit before Task 1 through current HEAD. Fix every Critical and Important finding or provide technical evidence for rejecting it.

- [ ] **Step 4: Commit review fixes and documentation**

```bash
git add -A
git commit -m "fix: address onboarding and console review findings"
```
