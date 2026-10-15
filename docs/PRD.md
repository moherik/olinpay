# openpay.id — Project Specification and Agent Onboarding

This document records the product intent, design decisions, architecture, implementation state, and known limitations of `openpay.id`. Read it together with `README.md` before changing behavior. Treat the repository as source of truth if this context becomes stale.

## 1. Product intent

`openpay.id` is an open-source payment orchestration service for Indonesian businesses. A merchant integrates with one API; OpenPay translates the merchant's unified payment requests to one or more PJP/payment-gateway APIs, routes requests, normalizes responses and callbacks, persists transaction state, and sends signed status webhooks to the merchant.

It is an orchestration layer—not a PJP, acquiring service, wallet, ledger, or settlement bank. The main goal is to let business systems switch/select payment gateways without embedding each gateway's API and callback semantics.

The four logical components requested in the initial brainstorming are all in one Go binary today (not separate services):

1. **API interface** — merchant-facing create, inquiry/get, list and cancel operations.
2. **Core engine** — translation boundary, routing/failover, transaction state and event orchestration.
3. **Provider engine** — one adapter per PJP, all satisfying one public interface.
4. **Webhook engine** — inbound PJP callbacks and outbound merchant notifications/retries.

## 2. Decisions from brainstorming

| Concern | Decision | Context |
| --- | --- | --- |
| Language | Go | User requirement. |
| Module | `github.com/moherik/openpay.id` | Actual module path in `go.mod`; use this in imports. |
| HTTP framework | chi | Used by the API and webhook HTTP servers. |
| Storage | PostgreSQL via pgx | Durable transactions and outbound delivery retries. |
| Public adapter contract | `pkg/provider` | Third-party adapters must not import `internal`. |
| Initial provider scope | Mock provider plus interface | Later user requests added Winpay, Midtrans, Xendit and DOKU as concrete, real-world examples. |
| Provider registration | `provider.Register(name, factory)` from adapter `init()` | Similar to `database/sql` driver registration; binary blank-imports each shipped adapter. |
| Routing | Pluggable strategy; built-in `priority`, `round_robin` | Strategies order eligible candidates; create tries them as failover. |
| Idempotency | `(merchant_id, reference_id)` | A repeated create request should return the existing transaction, not charge again. |
| Outbound webhooks | Persist before delivery; retry with exponential backoff | Avoid losing events across process restarts. |
| Testing | In-memory repositories and `httptest` | Routine tests should not require a database or provider credentials. |
| Winpay API | Bank Indonesia SNAP API | Adapter is a worked example, not a claim that every gateway uses SNAP. |
| Midtrans API | Midtrans BI-SNAP Core API | Asymmetric-signature + B2B token auth; a distinct product from the legacy Core API and the hosted Snap checkout. |
| Xendit API | Xendit Payments API v3 (`/v3/payment_requests`) | One payment-request endpoint per instrument; v3 is Xendit's recommended API over v2/legacy products. |
| DOKU API | Bank Indonesia SNAP API | Symmetric-signature + B2B token auth; per-bank VA paths. |

Known architectural tradeoff: create currently calls the external gateway before persisting the transaction. A successful gateway call followed by a local database failure can leave an external payment without a local row. See core comments; do not casually reorder operations without addressing idempotency/reconciliation.

## 3. Architecture and package boundaries

```text
Merchant system ──> internal/api ──> internal/core ──> core.Router
       ^                                      │                │
       │                                      │                v
       │                                      │         pkg/provider/<adapter> ──> PJP
       │                                      ^                                      │
       └── signed outbound webhook <── dispatcher <── receiver <── callback ─────────┘

API/core ──> repository interfaces ──> PostgreSQL implementation
```

| Path | Responsibility |
| --- | --- |
| `cmd/openpay` | `serve`, `migrate`, `seed`, `version`; dependency wiring and blank imports for providers. |
| `internal/api` | chi routes, auth, DTO validation/conversion, API errors, provider discovery. |
| `internal/core` | Orchestration, merchant scope, idempotency, routing/failover, transaction state, callback reconciliation. Must not know HTTP or SQL details. |
| `internal/domain` | Internal entities/errors and routing configuration. |
| `internal/repository` | Persistence interfaces and query filters. |
| `internal/repository/postgres` | pgx repositories and SQL. |
| `internal/testutil/memory` | In-memory repositories for tests. |
| `internal/webhook` | PJP callback receiver and persisted outbound dispatcher/retries. |
| `internal/platform` | Logging, IDs, HTTP helpers, migration runner; no business rules. |
| `pkg/provider` | Public provider contract, unified types/statuses/errors, registry and webhook request. |
| `pkg/provider/mock` | Deterministic in-memory gateway and simple reference adapter. |
| `pkg/provider/midtrans` | Midtrans BI-SNAP adapter (VA, QRIS, eWallet) with asymmetric-signature + B2B token auth. |
| `pkg/provider/winpay` | Winpay SNAP adapter and cryptographic helpers. |
| `pkg/provider/xendit` | Xendit Payments API v3 adapter (VA, QRIS, eWallet, retail outlet, pay later, card). |
| `pkg/provider/doku` | DOKU SNAP adapter (VA, QRIS, eWallet) with symmetric-signature + B2B token auth. |
| `migrations` | Forward-applied SQL migrations. |

`internal/` is private to this Go module. Do not make `pkg/provider` import an `internal` package. Keep transport, persistence and gateway-specific logic in their owning layers.

## 4. Main flows

### Create

1. Merchant calls `POST /v1/payments` with `X-API-Key` or Bearer key.
2. API validates/converts DTO and calls `core.Engine.CreatePayment`.
3. Core validates merchant/request and checks the `(merchant, reference_id)` idempotency key before contacting a gateway.
4. Router selects eligible providers by merchant route and capabilities. An explicit `provider` selects one adapter but still validates method/channel support.
5. Strategy orders candidates. `priority` sorts ascending priority stably; `round_robin` rotates start per merchant/method. Core tries candidates in order on create failure.
6. Provider maps request to gateway API and maps response to unified fields (status, amount, provider reference, action/payment code, expiry, raw payload).
7. Core persists transaction and returns it. Terminal/instant result can trigger outbound notification.

Failover after an ambiguous gateway error can create duplicates unless gateway-side idempotency is reliable. Keep error classification and reference mapping conservative.

### Inquiry and cancel

`GET /v1/payments/{id}` reads stored state; `?sync=true` asks provider for fresh state. `POST /v1/payments/{id}/cancel` requests cancellation/expiry if supported. API/core must always scope transactions to the authenticated merchant.

### Callback and notification

PJP `POST /webhooks/{provider}` → receiver reads bounded raw body → optional provider signature verification → provider parses callback → core correlates and updates transaction → outbound notification persisted → dispatcher sends signed merchant webhook with retry/backoff. On success receiver uses optional `WebhookResponder` where implemented; otherwise sends its generic acknowledgement.

The PJP callback URL (gateway → OpenPay) is distinct from merchant `callback_url` (OpenPay → merchant).

## 5. API and unified vocabulary

Merchant API routes (all `/v1` routes are authenticated):

| Method | Path | Function |
| --- | --- | --- |
| POST | `/v1/payments` | Create/idempotently retrieve payment. |
| GET | `/v1/payments/{id}` | Get payment; `?sync=true` refreshes. |
| GET | `/v1/payments` | List with merchant-scoped filters. |
| POST | `/v1/payments/{id}/cancel` | Cancel/expire pending payment. |
| GET | `/v1/providers` | Discover loaded providers/capabilities. |
| GET | `/v1/me` | Authenticated merchant profile. |
| GET | `/healthz`, `/readyz` | Health endpoints. |

Webhook HTTP surface: `POST /webhooks/{provider}`, public to gateway but authenticated according to provider signature rules; webhook server also has `/healthz`.

Create request fields include `reference_id`, integer `amount`, `currency` (defaults IDR), `method`, optional `channel`, optional `provider`, `description`, `expiry_seconds`, `customer`, `items`, and string map `metadata`. IDR amount is integer rupiah; never use float for money.

Unified methods: `virtual_account`, `qris`, `ewallet`, `retail_outlet`, `card`, `paylater`, `direct_debit`. Statuses: `pending`, `paid`, `expired`, `cancelled`, `failed`, `refunded`, `unknown`. See `pkg/provider/status.go` for exact constants.

## 6. Provider interface and adding an adapter

`pkg/provider/contract.go` is the compatibility boundary. The `Provider` interface consists of identity/capabilities and lifecycle operations: `CreatePayment`, `Inquiry`, `Cancel`, `ParseWebhook`. Providers must be concurrency-safe. Important unified types are in `pkg/provider/types.go`; callback request/event are in `pkg/provider/webhook.go`.

Optional extensions:

- `SignatureVerifier`: authenticate raw inbound callback before parsing.
- `WebhookResponder`: supply exact acknowledgement status/body for gateways that require it.
- `HealthChecker`: report provider readiness.

Adapter author checklist:

1. Implement at `pkg/provider/<name>` and import only the public contract (plus normal external dependencies).
2. Give it stable lowercase URL-safe `Name`, accurate capabilities, constructor `New(provider.Config)`, and `init()` registration.
3. Blank-import it in `cmd/openpay/main.go`, or it will not be linked/registered in this binary.
4. Use context-aware HTTP, explicit timeout, bounded response body, and concurrency-safe state.
5. Preserve merchant `ReferenceID`; set gateway ID in `ProviderReference`; normalize every gateway status and amount.
6. Wrap errors with `provider.NewError` / `provider.WrapError` and relevant sentinel so `errors.Is` still works.
7. Keep `ParseWebhook` pure (no I/O, DB, or state mutation); preserve raw body until signature check.
8. Add stub-server tests for request/response mapping, statuses, error mapping, callbacks, signatures and acknowledgement.
9. Update `.env.example`, README/spec, and adapter capability tests.
10. Never put secrets in source, fixtures, logs, or committed `.env`.

Factories are registered with `provider.Register`; `provider.NewRegistry()` snapshots registered factories. Runtime enable list is `OPENPAY_PROVIDERS`; provider config environment convention is `OPENPAY_PROVIDER_<NAME>_<KEY>`, keys normalized to lowercase. `Config.Get(key, default)` takes two arguments; `Required(key)` validates mandatory config.

## 7. Gateway adapter implementations

### Winpay (SNAP API)

The adapter at `pkg/provider/winpay` targets documented Winpay SNAP endpoints:

| Method | Operations |
| --- | --- |
| VA | `/v1.0/transfer-va/create-va`, `inquiry-va`, `status`, `delete-va` |
| QRIS | `/v1.0/qr/qr-mpm-generate`, `qr-mpm-query`, `qr-mpm-cancel` |
| eWallet | `/v1.0/debit/payment-host-to-host`, `status`, `cancel` |

It uses SNAP RSA-SHA256 PKCS#1 v1.5 signing, compact JSON body hash and endpoint path; headers include `X-TIMESTAMP`, `X-SIGNATURE`, `X-PARTNER-ID`, `X-EXTERNAL-ID`, and `CHANNEL-ID`. Public/private RSA key accepts PEM or base64 DER. Callback path is part of signature construction: receiver populates `WebhookRequest.Path`; `callback_path` config can override it. Winpay ack codes vary by callback product and are supplied through `WebhookResponder`.

Config keys: `partner_id` (required), `private_key` (required), `public_key` (strongly required in production for inbound verification), `base_url` (sandbox default; production URL must be configured), `channel_id` (default `WEB`), `callback_url` (needed for eWallet `PAY_NOTIFY`), `callback_path` (optional), `default_expiry`, `timeout`.

### Known Winpay limitation: process-local correlation

Winpay inquiry/status/cancel require gateway-specific values such as `contractId`, channel, and VA number. The current unified inquiry/cancel request does not carry all these. Adapter temporarily caches them in process memory after `CreatePayment`, with 24-hour TTL. Restart or another replica loses the mapping; inquiry/cancel can fail, and callback enrichment from contract ID may fail. This is a production-readiness gap. Correct solution: persist provider correlation metadata with transaction, update migration/domain/repository/core/provider calls with compatibility considered, and test restart/multi-instance behavior. Do not disguise this limitation or introduce an unbounded cache.

Channel codes and which method they belong to must be verified against current Winpay account documentation; capability lists are advertised, and matching trims whitespace and ignores case.

### Midtrans (BI-SNAP API)

The adapter at `pkg/provider/midtrans` targets Midtrans' BI-SNAP Core API (Bank Indonesia's *Standar Nasional Open API Pembayaran*), not the legacy Core API (`/v2/charge`) nor the hosted Snap checkout product:

| Method | Operations |
| --- | --- |
| Virtual Account | `POST /v1.0/transfer-va/create-va`, `POST /v1.0/transfer-va/status`, `POST /v1.0/transfer-va/delete-va` |
| QRIS | `POST /v1.0/qr/qr-mpm-generate`, `POST /v1.0/qr/qr-mpm-query`, `POST /v1.0/qr/qr-mpm-cancel` |
| eWallet | `POST /v1.0/debit/payment-host-to-host`, `POST /v1.0/debit/status`, `POST /v1.0/debit/cancel` |

Authentication uses two signatures. A B2B access token is obtained from `POST /v1.0/access-token/b2b`, signing `clientID|timestamp` with RSA-SHA256 (PKCS#1 v1.5). Every transactional request then carries an HMAC-SHA512 signature (keyed by `client_secret`) over `HTTPMethod:EndpointUrl:AccessToken:Lowercase(HexEncode(SHA-256(minify(body)))):Timestamp`, plus `Authorization: Bearer <token>`, `X-PARTNER-ID`, `X-EXTERNAL-ID` and `CHANNEL-ID` headers. The token is cached and refreshed shortly before its 900s expiry, and dropped on a 401.

`X-EXTERNAL-ID` is Midtrans' per-request numeric idempotency key (unique for 24 hours). The adapter generates a fresh value for each request and replays the create-time value as `originalExternalId` on status/cancel calls.

Inbound notifications are signed by Midtrans with its own RSA key (SHA256withRSA over `HTTPMethod:EndpointUrl:Lowercase(HexEncode(SHA-256(minify(body)))):X-TIMESTAMP`, no access token) and verified with the configured `public_key`; when `public_key` is not set the adapter rejects callbacks rather than trusting them. The SNAP numeric status (`00` paid, `01`/`02`/`03` pending, `04` refunded, `05` cancelled, `06`/`07`/`09` failed, `08` expired) maps onto unified statuses; the VA payment notification uses `additionalInfo.paymentFlagStatus` and falls back to paid.

Because Midtrans' status/cancel endpoints are addressed by gateway identifiers (the VA number, the original reference numbers, the create-time `X-EXTERNAL-ID`) that the unified contract does not carry between calls, the adapter caches them in process memory after `CreatePayment`, with a 24-hour TTL. Restart or another replica loses the mapping and inquiry/cancel fails with `ErrNotFound`. This is a production-readiness gap, the same one as the Winpay and DOKU adapters; the correct fix is to persist provider correlation metadata with the transaction.

Config keys: `client_id` (required, `X-CLIENT-KEY`), `client_secret` (required, HMAC key), `private_key` (required; PEM or base64 DER), `public_key` (Midtrans RSA public key; empty rejects callbacks), `base_url` (sandbox default `https://merchants.sbx.midtrans.com`; production is `https://merchants.midtrans.com`), `partner_id` (`X-PARTNER-ID`, defaults to `client_id`), `merchant_id` (required for VA; default QRIS merchant id), `partner_service_id` (8-char left-space-padded BIN, required for VA), `channel_id` (`CHANNEL-ID`, default `12345`), `device_id` (optional `X-DEVICE-ID`), `qris_acquirer` (QRIS acquirer, default `gopay`), `default_expiry` (default 24h), `timeout`. Two per-payment values travel in `metadata`: `return_url` (e-wallet `PAY_RETURN` url) and `sub_merchant_id` (QRIS merchant id, unique per (sub)merchant).

Known limitations: BI-SNAP covers only virtual account, QRIS and e-wallet, so retail outlet, pay later and card are **not** supported (a deliberate scope reduction from the legacy adapter); capability/channel codes must be verified against the current Midtrans account; the exact `EndpointUrl` Midtrans folds into the notification signature is documented only as "EndpointUrl" and no endpoint header is sent, so verification tries the received path and the three documented notification paths (this has not been verified against a live gateway); refunds are not first-class (the platform has no refund operation yet).

### Xendit (Payments API v3)

The adapter at `pkg/provider/xendit` targets documented Xendit Payments API v3 endpoints:

| Method | Operations |
| --- | --- |
| VA / QRIS / eWallet / pay later / card | `POST /v3/payment_requests` (`type PAY`, channel-specific `channel_code`) |
| Retail outlet | `POST /v3/payment_requests` (`type REUSABLE_PAYMENT_CODE`, `ALFAMART`/`INDOMARET`) |
| Any | `GET /v3/payment_requests/{payment_request_id}`, `POST /v3/payment_requests/{payment_request_id}/cancel` |

Authentication is HTTP Basic with the secret key as username and an empty password, plus an `api-version` header (`2024-11-11` by default). Callbacks are authenticated by comparing the shared `x-callback-token` header in constant time; when `callback_token` is not configured the adapter rejects callbacks rather than trusting them. The payment request status (`REQUIRES_ACTION`, `ACCEPTING_PAYMENTS`, `AUTHORIZED`, `SUCCEEDED`, `FAILED`, `EXPIRED`, `CANCELED`) and the payment status are normalized onto unified statuses; refund events (`refund.succeeded`/`refund.failed`) are mapped by event name.

Because Xendit keys a payment request by its own `payment_request_id` (returned as `ProviderReference`), Inquiry and Cancel address the request directly, so no process-local correlation cache is needed. The merchant `reference_id` is passed through and echoed on callbacks.

Config keys: `secret_key` (required; `xnd_development_...` for test mode, `xnd_production_...` for live), `base_url` (default `https://api.xendit.co`; test and live share the host and are selected by the key), `api_version` (default `2024-11-11`), `callback_token` (webhook verification token; empty rejects callbacks), `return_url` (where the payer returns after redirect flows), `default_expiry` (applied to VAs and QRIS only), `timeout`.

Known limitations: capability/channel codes must be verified against the current Xendit account (availability is account-specific); card payments require a saved payment token (`metadata.payment_token_id`, or `metadata.card_token`); the v3 API exposes no forced-expiry operation, so a Cancel with reason `expire` is performed as a cancel; channel-specific fields can be supplied through `metadata.channel_properties` (a JSON object), which the adapter merges over its generated properties.

### DOKU (SNAP API)

The adapter at `pkg/provider/doku` targets documented DOKU SNAP endpoints:

| Method | Operations |
| --- | --- |
| Virtual Account | `POST .../transfer-va/create-va` (per-bank path), `POST /orders/v1.0/transfer-va/status`, `DELETE .../transfer-va/delete-va` |
| QRIS | `POST /snap-adapter/b2b/v1.0/qr/qr-mpm-generate`, `.../qr-mpm-query`, `.../qr-expire` |
| eWallet | `POST /direct-debit/core/v{1,2}/debit/payment-host-to-host`, `POST /orders/v1.0/debit/status` |

Authentication uses DOKU's default "Symmetric Signature with Get Token" scheme. A B2B access token is obtained from `POST /authorization/v1/access-token/b2b`, signing `clientID|timestamp` with RSA-SHA256 (PKCS#1 v1.5). Every transactional request then carries an HMAC-SHA512 signature (keyed by `client_secret`) over `HTTPMethod:EndpointUrl:AccessToken:Lowercase(HexEncode(SHA-256(minify(body)))):Timestamp`, plus `Authorization: Bearer <token>`, `X-PARTNER-ID`, `X-EXTERNAL-ID` and `CHANNEL-ID` headers. The token is cached and refreshed shortly before its 900s expiry, and dropped on a 401 so the next call fetches a fresh one.

DOKU does not use one unified VA endpoint: the create-va path and the `additionalInfo.channel` enum differ per issuing bank (BCA, BRI, BNI, BNC, Mandiri, Permata, CIMB, BSI, Danamon, Maybank, Sinarmas, BTN, BJB). The adapter carries a per-bank table; `va_path` config overrides it. The delete-va path is derived by replacing `create-va` with `delete-va`.

Callbacks are authenticated with HMAC-SHA512 over `HTTP-METHOD:RELATIVE-URL:ACCESS-TOKEN:HEX(SHA256(requestBody)):TIMESTAMP` using the merchant's `client_secret`. Because DOKU does not document precisely which access token is folded into the notification signature, the adapter tries each candidate (empty, then the cached B2B token) in constant time. Two notification shapes are recognized: the VA payment notification (`POST /v1/transfer-va/payment`, correlated by `trxId` and the VA number) and the direct-debit notification (`POST /v1.0/debit/notify`, correlated by `originalPartnerReferenceNo` and mapped from `latestTransactionStatus`). `WebhookResponder` replies with the SNAP acknowledgement body (`2002500` for VA, `2005600` otherwise).

Config keys: `client_id` (required), `client_secret` (required), `private_key` (required RSA, PEM or base64 DER), `base_url` (default `https://api-sandbox.doku.com`; production `https://api.doku.com`), `partner_service_id` (8-digit left-space-padded BIN, required for VA), `merchant_id` / `terminal_id` / `postal_code` (required for QRIS), `callback_url` (e-wallet PAY_RETURN url), `channel_id` (default `H2H`), `va_path` (per-bank path override), `default_expiry`, `timeout`.

Known limitations: like Winpay, DOKU addresses status/delete by gateway-specific identifiers (VA number, original reference numbers) that the unified contract does not carry, so the adapter keeps a process-local correlation cache after `CreatePayment` (24-hour TTL); a restart or another replica loses it. Channel availability and the per-bank VA paths must be verified against the current DOKU account; no live sandbox verification has been performed. DOKU exposes no cancel endpoint for e-wallet host-to-host payments, so `Cancel` returns `ErrUnsupportedOperation` for that method.

## 8. Routing, state and persistence

- Router filters providers by enabled merchant config and `Capabilities()`.
- With no explicit merchant provider list, all registered matching providers can be candidates (use explicit production routes).
- Explicit provider name skips strategy ordering but not capability validation.
- Strategies only order candidates; core executes them as an ordered create failover chain.
- Repository interfaces are in `internal/repository`, PostgreSQL implementation in `internal/repository/postgres`, and test doubles in `internal/testutil/memory`.
- Migration runner is `internal/platform/migrate`; initial schema is `migrations/0001_init.sql`.
- Outbound webhook records are persisted before delivery; delivery ID remains stable for retries and is suitable for recipient idempotency.

Schema changes: add a new forward-only migration; update domain, repository interfaces, PostgreSQL scanners/writers, memory implementation, and tests. Consider data backfill/defaults, transaction boundaries, concurrency, and deployed migration state. Do not edit already-deployed migrations as if they had never run.

## 9. Security and financial correctness

- Set a stable `OPENPAY_API_KEY_PEPPER` in production; rotating it invalidates stored key hashes.
- `.env` is local/ignored; `.env.example` must contain only placeholders.
- Never disable production gateway callback verification (Winpay with no `public_key` skips verification; Midtrans rejects callbacks when `public_key` is unset; Xendit rejects callbacks when `callback_token` is unset; DOKU rejects callbacks when `client_secret` is unset).
- Verify signature against the required raw/canonical bytes before interpreting callback payload.
- Enforce merchant ownership on get/list/cancel. Ensure callbacks correlate only to the intended provider and transaction.
- Use integer money; reject/handle unsupported currency/precision deliberately.
- Duplicate callbacks and status transitions must be idempotent; do not cause duplicate merchant effects.
- Respect context cancellation, HTTP timeouts, body limits; do not log credentials, secrets, private keys, or unnecessary PII.
- Provider API calls are traced through the application logger: metadata (provider, method, URL, status, duration) at info, request/response headers and bodies at debug. Sensitive headers and JSON fields are redacted before logging and bodies are size-capped.
- Be explicit about partial failure: gateway success with local persistence failure, timeout with unknown gateway outcome, and retries are core payment correctness concerns.

## 10. Build, run and test

Go version is specified in `go.mod` (currently Go 1.25.0); Docker is used for local PostgreSQL.

```bash
cp .env.example .env
make up
make migrate
make seed
make run

go build ./...
go vet ./...
go test ./...
go test -race ./...
make check
```

Most tests are DB-free. `pkg/provider` tests cover the shared request/response tracing transport (metadata at info, redacted bodies at debug, no leaks of authorization headers or token fields). Winpay tests use `httptest` with generated RSA keys; the fake gateway checks signatures on outgoing calls. Midtrans tests use `httptest` against a stub BI-SNAP API that checks the B2B token and HMAC-SHA512 request signatures, covering create/inquiry/cancel for VA/QRIS/eWallet, token caching, RSA notification verification and status mapping. Xendit tests use `httptest` against a stub Payments API that checks Basic Auth and the `api-version` header, covering every supported method plus create/inquiry/cancel, `x-callback-token` verification and event/status mapping. DOKU tests use `httptest` against a stub SNAP API that checks the B2B token and HMAC-SHA512 request signatures, covering create/inquiry/cancel for VA/QRIS/eWallet, token caching, and notification signature verification. Live gateway sandbox testing requires valid credentials and current account-specific endpoint details; passing unit tests is not proof of live integration.

## 11. Current status and roadmap

Implemented: merchant API, core engine and routing (`priority`, `round_robin`), mock adapter, Winpay SNAP adapter (VA/QRIS/eWallet), Midtrans BI-SNAP adapter (VA/QRIS/eWallet), Xendit Payments API v3 adapter (VA/QRIS/eWallet/retail outlet/pay later/card), DOKU SNAP adapter (VA/QRIS/eWallet), provider API request/response tracing, provider registry/contract, inbound webhooks, signed/retried outbound webhooks, PostgreSQL persistence/migrations, CLI, and tests.

Known/planned: persist Winpay/Midtrans/DOKU gateway correlation values; live sandbox verification; additional gateway adapters; first-class refunds; admin API; production queue/metrics/dashboards (provider-request tracing already ships); stronger recovery for provider success followed by DB failure. Do not claim roadmap items are implemented.

## 12. Instructions for a future coding agent

1. Read this document and `README.md`; inspect exact target files and `git status` first.
2. Treat uncommitted changes as user work; never reset/revert unrelated changes.
3. Identify the owning layer and preserve boundaries: API HTTP, core orchestration, repository persistence, adapter gateway details.
4. Preserve the public contract unless the task requires a change. For contract changes, explain compatibility impact and add tests.
5. For payment changes, analyze idempotency, retries, ambiguous outcomes, duplicate callbacks, status transitions, currency/amount validation and multi-instance behavior—not only the happy path.
6. Prefer official gateway documentation and captured fixtures. Do not silently invent undocumented payload fields. Keep live network tests opt-in.
7. Run gofmt, `go build ./...`, `go vet ./...`, relevant tests; run full tests/race tests for cross-cutting changes when practical.
8. Update this document and README when architecture, API, config or known limitations change.
9. Report accurately what changed and was tested; do not claim live gateway or production verification unless actually performed.
