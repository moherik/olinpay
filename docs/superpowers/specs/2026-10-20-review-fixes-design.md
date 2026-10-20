# Review Fixes Design

## Intent

Close the security, partial-failure, validation, routing-editor, local-development, mobile, and documentation gaps found in the review of commits `87b04a6..32f9557` without changing the payment API's successful behavior.

The browser is the only first-administrator bootstrap path. Setup must be safe under concurrent requests, must remain useful before any provider is configured, and must not create duplicate merchants when API-key creation fails.

## Goals

- Guarantee that at most one first owner can be created, including across several application instances.
- Remove the `create-admin` CLI command and all claims that it is an alternative bootstrap path.
- Allow the API/admin console to boot with zero live payment-provider instances.
- Keep all setup cards visible and navigable; preserve a successfully created merchant when initial API-key creation fails and let the owner finish without a key.
- Enforce the AW merchant-ID contract in application persistence and PostgreSQL.
- Reject routing graphs containing reachable cycles instead of silently truncating them.
- Make `make dev` distinguish healthy processes, occupied ports, failed starts, and readiness timeouts.
- Give the console a deliberate mobile navigation state and 44px mobile touch targets.
- Complete visible AwsemPay branding and correct merchant-ID examples.

## Non-goals

- No migration or compatibility support for historical `mch_...` merchant IDs.
- No live gateway verification.
- No redesign of routing semantics, provider adapters, payment idempotency, callbacks, or webhook delivery.
- No durable storage of plaintext one-time secrets in browser storage.
- No general-purpose process supervisor or production deployment tooling.

## 1. Atomic browser bootstrap

Extend `repository.AdminUserRepository` with `CreateFirst(ctx, user) error`.

- PostgreSQL implements it in one transaction. The transaction takes a fixed transaction-scoped advisory lock, counts `admin_users`, inserts only when the count is zero, and commits. Any request that observes an existing user after acquiring the lock returns `domain.ErrConflict`.
- The in-memory repository performs the same check and insert under its existing mutex.
- `POST /admin/api/setup` hashes and validates input as today, then calls `CreateFirst`; `domain.ErrConflict` maps to `409 setup_completed` rather than the generic conflict envelope.
- `GET /admin/api/console-status` continues using `Count`; it is informational, not the authority for creation.
- Add a concurrency regression test that releases several setup requests together and proves exactly one owner is created while every loser receives `409 setup_completed`.

Remove `create-admin` from command dispatch, help text, implementation, CLI tests, README, PRD, and admin copy. Later accounts are created only through the authenticated Users API.

## 2. Setup merchant partial failure

The merchant step becomes an explicit two-operation state machine:

1. Create the merchant.
2. Save `{id, name, environment, webhookSecret}` in component state immediately.
3. Attempt to create the initial API key for that merchant.

If operation 2 succeeds and operation 3 fails:

- Keep the merchant state and webhook secret visible.
- Show the key error beside a `Continue without a key` primary action.
- Do not retry key creation in the wizard. Tell the owner to inspect the authenticated API Keys screen and create or revoke keys there after setup.
- Do not render or enable the original merchant-creation submit path again.
- Keep Back available; reopening the step must retain the same partial state during the current page lifetime.

When both operations succeed, show both one-time secrets and continue as today. Plaintext secrets remain in React memory only; they are never placed in `localStorage` or `sessionStorage`. While an unacknowledged secret is visible, register a `beforeunload` warning so an accidental refresh does not silently discard it. If the user deliberately leaves despite the warning, the secret remains unrecoverable by design.

Extract the transition logic into a small pure setup-state module and test these transitions with Node's built-in test runner (no browser-test dependency): initial → merchant-created → key-failed → continued-without-key, plus successful key creation, skip/back, and reopen behavior.

An ambiguous API-key response can mean the key committed even though the browser did not receive its plaintext value. The wizard does not automatically retry or revoke in this case. The API Keys screen will expose the resulting key row and prefix so the owner can revoke it before creating a replacement manually.

## 3. Boot without providers

`buildRegistry` returns a valid empty registry after environment and database configuration attempts. It no longer treats `registry.Len() == 0` as startup failure.

- Admin and setup endpoints remain available.
- Provider catalog/configuration continues using registered adapter factories.
- Payment creation with no eligible live provider follows the engine's existing unroutable/no-provider error path; it must not panic.
- Replace the existing empty-registry failure test with tests proving clean boot and deterministic payment failure until a provider is configured.
- Startup logs emit a warning that payments cannot route yet, rather than terminating the process.

## 4. Strict AW merchant IDs

The accepted shape is exactly `AW` followed by eight characters from the existing unambiguous alphabet (`A-H`, `J-N`, `P-Z`, `2-9`), ten characters total.

- Rename the shape helper to a validation-oriented API and return a validation error from both PostgreSQL and in-memory merchant `Create` methods before writing.
- Keep generation in `internal/platform/id`; all creation paths continue using it.
- Add migration `0003_merchant_id.sql` with a strict PostgreSQL `CHECK` constraint equivalent to the application validator.
- The migration does not rewrite, retain, or delete historical IDs. It intentionally fails if incompatible rows still exist; operators must clean/reset those rows explicitly.
- Tests cover valid generation and rejection of wrong prefix, alphabet, casing, and length at the persistence boundary.

## 5. Routing graph cycle rejection

Before compilation, run a directed DFS over edges reachable from the entry node using white/gray/black visitation states.

- An edge to a gray node is a cycle. Add an issue to the target node: `Routing flow contains a cycle — remove the loop.`
- Detect cycles through the fallback path and either condition branch, including cycles that cross branches.
- Deduplicate cycle issues per node.
- Keep the current visited sets as termination guards, but never rely on them as validation.
- Unit tests cover fallback, match, no-match, and acyclic shared-tail graphs.

## 6. Reliable local dev process management

Move process orchestration from a long Make recipe to `scripts/dev.sh`; `make dev` invokes it and `make dev-stop` stops only processes started by that script.

The script:

- Uses `/tmp/awsempay-dev/` for PID and log files.
- Checks readiness first. A healthy existing API/dashboard is reused even if it was started elsewhere.
- If readiness fails but the port is occupied, exits nonzero and reports the occupied port instead of launching a duplicate.
- Removes stale PID files only after proving their process is gone.
- Starts missing services, records their PIDs, and polls readiness with a bounded timeout rather than sleeping a fixed five seconds.
- If a child exits or readiness times out, exits nonzero and prints the relevant log location.
- `dev-stop` sends TERM only to matching live PIDs recorded by this script, waits briefly, and removes the PID files. It never kills arbitrary port owners.

The script targets Linux, matching the repository's Docker/Make development environment. It may use Linux process-group and port-inspection facilities rather than claiming macOS portability. It exposes command, URL, port, timeout, and state-directory overrides so shell tests can use harmless fake servers and temporary directories. Tests cover healthy reuse, occupied-unhealthy failure, successful readiness, child exit, timeout, stale PID cleanup, process-group stop, and stop ownership.

`reset-onboarding` continues to call `make dev`; it now fails if either service fails readiness.

## 7. Mobile shell

Desktop keeps the approved full-width two-row shell. Below the content-driven breakpoint:

- Hide the horizontal nine-item navigation row.
- Show a labeled `Menu`/`Sections` control with at least a 44×44px hit area.
- Open the existing accessible dialog/menu primitives containing every destination as 44px rows; active location remains textually and visually identified.
- Environment choices and the account trigger receive 44px mobile hit areas, reverting to the dense 32px console scale on desktop.
- Keep the sticky mobile header compact and verify no horizontal page overflow.

The environment wash remains amber for sandbox and primary blue for production in both themes; color is supplementary because the selected environment remains named in the switch.

## 8. Documentation and branding

- Change visible OpenAPI product title, description, and maintainer display copy to AwsemPay.
- Preserve technical compatibility identifiers: Go module path, `OPENPAY_*` variables, `X-OpenPay-*` headers, database names, and stable API paths.
- Replace every illustrative merchant ID with a valid ten-character example such as `AW9X2K4T7Z`.
- Document the strict migration behavior and browser-only bootstrap.
- Update README, PRD, admin README, OpenAPI, command help, and relevant comments together.

## Error and security behavior

- Setup race losers always receive the same non-enumerating `409 setup_completed` response.
- Repository/database errors remain `500` and are logged server-side with their underlying cause; client responses do not reveal SQL details.
- One-time secrets are displayed only in the page that created them and are never logged or persisted in browser storage.
- Empty provider state disables payment routing by existing engine behavior but never blocks administrative recovery.

## Testing and delivery

Follow test-driven development per fix: add one failing regression test, make the smallest implementation change, and run the narrow test before moving to the next item.

Final verification:

- `gofmt` on changed Go files
- `go build ./...`
- `go vet ./...`
- `go test ./...`
- `go test -race ./...`
- `npm --prefix admin run lint`
- `npm --prefix admin run build`
- setup concurrency test under PostgreSQL when available
- shell process-management tests
- narrow/mobile shell check for keyboard navigation, touch targets, and overflow

## Builder calls

- Choose the fixed advisory-lock key and keep it private to the PostgreSQL repository.
- Choose the exact content breakpoint where the horizontal nav no longer fits.
- Choose pure setup-state function names and test organization consistent with the existing admin source tree.
- Keep changes split into focused commits so security, onboarding, routing validation, process tooling, and UI can be reviewed independently.
