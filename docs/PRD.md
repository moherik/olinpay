# AwsemPay (`openpay.id`) Project Specification and Agent Onboarding

This document records the product intent, design decisions, architecture, implementation state, and known limitations of the AwsemPay platform (`openpay.id`). Read it together with `README.md` before changing behavior. Treat the repository as source of truth if this context becomes stale.

## 1. Product intent

`AwsemPay` is an open-source payment orchestration service for Indonesian businesses. A merchant integrates with one API; AwsemPay translates the merchant's unified payment requests to one or more PJP/payment-gateway APIs, routes requests, normalizes responses and callbacks, persists transaction state, and sends signed status webhooks to the merchant.

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
| Initial provider scope | Mock provider plus interface | Later user requests added Winpay, Midtrans (legacy Core API and BI-SNAP), Xendit and DOKU as concrete, real-world examples. |
| Provider registration | `provider.Register(name, factory)` from adapter `init()` | Similar to `database/sql` driver registration; binary blank-imports each shipped adapter. |
| Routing | Pluggable strategy; built-in `priority`, `round_robin` | Strategies order eligible candidates; create tries them as failover. |
| Merchant ID | `AW` prefix + 8 random characters, 10 total | Short, human-typed identifiers (e.g. `AW9X2K4T7Z`); no other format is supported. |
| Idempotency | `(merchant_id, reference_id)` | A repeated create request should return the existing transaction, not charge again. |
| Outbound webhooks | Persist before delivery; retry with exponential backoff | Avoid losing events across process restarts. |
| Testing | In-memory repositories and `httptest` | Routine tests should not require a database or provider credentials. |
| Winpay API | Bank Indonesia SNAP API | Adapter is a worked example, not a claim that every gateway uses SNAP. |
| Midtrans API (legacy) | Midtrans legacy Core API (`/v2/charge`) | HTTP Basic auth with the server key; order_id-addressed status/cancel; a distinct product from BI-SNAP and the hosted Snap checkout. |
| Midtrans API (BI-SNAP) | Midtrans BI-SNAP Core API | Asymmetric-signature + B2B token auth; registered as `midtrans-snap` so it coexists with the legacy Core API adapter. |
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
| `pkg/provider/midtrans` | Midtrans legacy Core API adapter (VA incl. Mandiri echannel, QRIS, eWallet, retail outlet) with HTTP Basic auth and order_id correlation. |
| `pkg/provider/midtrans-snap` | Midtrans BI-SNAP adapter (VA, QRIS, eWallet) with asymmetric-signature + B2B token auth. |
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
2. Give it stable lowercase URL-safe `Name` (hyphens allowed, so related products can register under distinct names), accurate capabilities, constructor `New(provider.Config)`, and `init()` registration.
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
| Modern Retail | `/v1.0/transfer-va/create-va`, `inquiry-va`, `status`, `delete-va` with `INDOMARET`, `ALFAMART`, or `FASTPAY` |
| QRIS | `/v1.0/qr/qr-mpm-generate`, `qr-mpm-query`, `qr-mpm-cancel` |
| eWallet | `/v1.0/debit/payment-host-to-host`, `status`, `cancel` |

It uses SNAP RSA-SHA256 PKCS#1 v1.5 signing, compact JSON body hash and endpoint path; headers include `X-TIMESTAMP`, `X-SIGNATURE`, `X-PARTNER-ID`, `X-EXTERNAL-ID`, and `CHANNEL-ID`. Public/private RSA key accepts PEM or base64 DER. One callback base URL can receive Winpay's product-specific path suffixes; the receiver preserves the complete request path and the adapter verifies it against the configured or documented callback paths. Winpay ack codes vary by callback product and are supplied through `WebhookResponder`.

Config keys: `partner_id` (required), `private_key` (required), `public_key` (strongly required in production for inbound verification), `base_url` (sandbox default; production URL must be configured), `channel_id` (default `WEB`), `callback_url` (needed for eWallet `PAY_NOTIFY`), `callback_path` (optional), `default_expiry`, `timeout`.

### Known Winpay limitation: process-local correlation

Winpay inquiry/status/cancel require gateway-specific values such as `contractId`, channel, and VA number. The current unified inquiry/cancel request does not carry all these. Adapter temporarily caches them in process memory after `CreatePayment`, with 24-hour TTL. Restart or another replica loses the mapping; inquiry/cancel can fail, and callback enrichment from contract ID may fail. This is a production-readiness gap. Correct solution: persist provider correlation metadata with transaction, update migration/domain/repository/core/provider calls with compatibility considered, and test restart/multi-instance behavior. Do not disguise this limitation or introduce an unbounded cache.

Channel codes and which method they belong to must be verified against current Winpay account documentation; capability lists are advertised, and matching trims whitespace and ignores case.

### Midtrans (legacy Core API)

The adapter at `pkg/provider/midtrans` targets Midtrans' legacy Core API (`POST /v2/charge`), the REST product addressed by `order_id`. It is distinct from the BI-SNAP adapter (`midtrans-snap`) and from the hosted Snap checkout product, and covers:

| Method | Operations |
| --- | --- |
| Virtual Account | `POST /v2/charge` (`payment_type bank_transfer` for Permata/BCA/BNI/BRI/CIMB/Danamon/BSI/SeaBank/Saqua, or `echannel` for Mandiri Bill Payment), `GET /v2/{order_id}/status` |
| QRIS | `POST /v2/charge` (`payment_type qris`), `GET /v2/{order_id}/status` |
| eWallet | `POST /v2/charge` (`payment_type gopay`/`shopeepay`), `GET /v2/{order_id}/status` |
| Retail outlet | `POST /v2/charge` (`payment_type cstore`), `GET /v2/{order_id}/status` |
| Cancel / expire | `POST /v2/{order_id}/cancel`, `POST /v2/{order_id}/expire` |

Authentication is HTTP Basic with the Midtrans `server_key` as username and an empty password. Midtrans reports most business failures with HTTP 200 and a non-2xx body `status_code` (e.g. `406` duplicate order id, `201` created), so the adapter validates the response-level `status_code` rather than the transport status and classifies it (401/403 unauthorized, 404 not found, 406/409 duplicate reference, other 4xx invalid request, else gateway). The status/cancel/expire endpoints are addressed by the `order_id` (the merchant `reference_id`), so no process-local correlation cache is needed; a `Reason` of `expire` (case-insensitively) selects the `/expire` endpoint over `/cancel`.

Inbound notifications are authenticated with `SHA512(order_id + status_code + gross_amount + server_key)`, recomputed in constant time against the `signature_key` field; when `server_key` is not configured the adapter rejects callbacks rather than trusting them. The textual `transaction_status` (plus `fraud_status` for card-style captures) maps onto unified statuses: `capture`/`settlement` paid, `pending`/`authorize` pending, `deny`/`failure` failed, `cancel` cancelled, `expire` expired, and the refund/chargeback family refunded.

Config keys: `server_key` (required; the Basic-auth secret and notification signature key), `base_url` (sandbox default `https://api.sandbox.midtrans.com`; production `https://api.midtrans.com`), `qris_acquirer` (default `gopay`), `gopay_callback_url` (optional), `shopee_pay_callback_url` (optional), `default_expiry` (default 24h), `timeout`. Per-payment `metadata` carries `va_number` (VA only), `return_url` (e-wallet; overrides the configured callback_url) and `qris_acquirer` (QRIS acquirer override).

Known limitations: channel availability is account-specific and has not been verified against the live Midtrans sandbox; refunds are not first-class (the platform has no refund operation yet).

### Midtrans (BI-SNAP API)

The adapter at `pkg/provider/midtrans-snap` targets Midtrans' BI-SNAP Core API (Bank Indonesia's *Standar Nasional Open API Pembayaran*), not the legacy Core API (`/v2/charge`) nor the hosted Snap checkout product. It is registered as `midtrans-snap` so it coexists with the legacy adapter:

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

### Admin API and console (the control plane)

- The admin API mounts under `/admin/api` on the API server (`internal/admin`): server-side sessions (`admin_sessions`, cookie `HttpOnly`/`SameSite=Lax`, 12h TTL), RBAC enforced per route with the hierarchy **owner > operator > tech**, and an append-only audit log.
- Provider credentials are **global per adapter+environment** (`provider_configs` rows), not per merchant; a merchant's "provider assignment" is its routing entries. Saving a config builds the instance named `<adapter>.<env>` and hot-replaces it in the running registry; it also replaces the env-var-built instance for that environment at startup. Env-var-configured instances keep their bare adapter name (environment unknown to the catalog) and remain the no-DB fallback.
- Provider secrets are write-only: `GET /providers` never returns them; `PUT config` with a blank secret keeps the stored value. Console responses normalize nil lists to `[]`, and `RoutingConfig` marshals a nil provider list as `[]` so client code cannot trip on `null`.
- One-time secrets (merchant webhook secret on create, API key raw value, invited console user's initial password) are returned once in the response `meta` and stored hashed/irretrievable afterwards.
- The console (`admin/`, Next.js) proxies `/admin/api/*` to the backend via a rewrite (`OPENPAY_BACKEND_URL`, default `http://localhost:8080`), so the session cookie stays same-origin; there is no demo data — unwired features (refunds, transaction event log, provider debug log) are flag-gated off through `/api/me` `flags` rather than rendered disabled.
- The browser wizard at `/setup` is the only first-owner bootstrap path. Gateway setup is optional, so the server starts with no provider configuration for first-install administration; payment creation returns `no_route` until a provider is configured. If merchant creation succeeds but automatic initial-key creation cannot be confirmed, retain the merchant and one-time webhook secret, then manage keys manually from the authenticated API Keys screen.
- The console routing page renders a **visual routing graph** (`admin/src/components/routing/routing-studio.tsx`): condition nodes branch on `amount` (integer), `method`, `channel`, `currency` or `metadata:<key>` — a condition's match branch may carry modify edits, a provider chain or a respond node, the no-match branch continues to the next condition; provider nodes after the conditions form the fallback chain, optionally ending in a respond node; modify nodes directly after the entry enrich every request before rule evaluation. Saved through `PUT /merchants/{id}/routing` with `strategy` `priority` or `round_robin` and ordered `rules` evaluated first-match-wins; a failed rule route (and non-matching payments) falls through the fallback chain; a matching rule with a `reject` answers the payment without a gateway call, and a config-level `reject` answers when the whole chain declines. The admin API validates the saved config (condition field/operator pairing, integer amounts, registered provider instances, modify op/key completeness, reject status 400..599 with code and message, no rule that both routes and rejects). A saved reject must be an error status; the API layer clamps stray values into 400..599, so a payment answered by routing can never ship as 2xx/3xx.
- Every payment runs in an environment (from the API key) and routing candidates are environment-scoped: DB-saved instances are `<adapter>.<env>` and never serve the other environment; env-var instances keep bare names and serve both. A `"provider"` field on create-payment accepts the bare adapter name and resolves to the environment instance (`"winpay"` + sandbox → `winpay.sandbox`); cross-environment forcing is a `no_route`.
- Admin schema is `migrations/0002_admin.sql` (`admin_users`, `admin_sessions`, `audit_log`, `provider_configs`, plus merchant routing storage changes). Bootstrap the first owner only through the browser console's first-install wizard (`POST /api/setup`, public only while no console account exists, then `409 setup_completed`); later accounts are created through the authenticated Users API. There is still no email/password-reset flow.

Schema changes: add a new forward-only migration; update domain, repository interfaces, PostgreSQL scanners/writers, memory implementation, and tests. Consider data backfill/defaults, transaction boundaries, concurrency, and deployed migration state. Do not edit already-deployed migrations as if they had never run. Migration `0003_merchant_id.sql` strictly rejects pre-existing merchant IDs outside `AW[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{8}`; incompatible rows must be reset or cleaned up before it can apply. It does not rewrite IDs automatically.

## 9. Security and financial correctness

- Set a stable `OPENPAY_API_KEY_PEPPER` in production; rotating it invalidates stored key hashes.
- `.env` is local/ignored; `.env.example` must contain only placeholders.
- Never disable production gateway callback verification (Winpay with no `public_key` skips verification; Midtrans BI-SNAP rejects callbacks when `public_key` is unset; Midtrans legacy rejects callbacks when `server_key` is unset; Xendit rejects callbacks when `callback_token` is unset; DOKU rejects callbacks when `client_secret` is unset).
- Verify signature against the required raw/canonical bytes before interpreting callback payload.
- Enforce merchant ownership on get/list/cancel. Ensure callbacks correlate only to the intended provider and transaction.
- Use integer money; reject/handle unsupported currency/precision deliberately.
- Duplicate callbacks and status transitions must be idempotent; do not cause duplicate merchant effects.
- Respect context cancellation, HTTP timeouts, body limits; do not log credentials, secrets, private keys, or unnecessary PII.
- Provider API calls are traced through the application logger: metadata (provider, method, URL, status, duration) at info, request/response headers and bodies at debug. Sensitive headers and JSON fields are redacted before logging and bodies are size-capped.
- Be explicit about partial failure: gateway success with local persistence failure, timeout with unknown gateway outcome, and retries are core payment correctness concerns.
- Console authentication is server-side: bcrypt password hashes, hashed session tokens, constant-time comparisons, login rate limiting; RBAC failures are 403, not silent no-ops. Never weaken the owner-only gates around provider credentials, users and API keys.

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
npm --prefix admin run test:setup
npm --prefix admin run test:console
npm --prefix admin run test:routing
bash scripts/dev_test.sh
```

`make dev` starts the local API and console together; `make dev-stop` stops the
supervised process groups. Stop waits for each owned group to exit, bounded by
`AWSEMPAY_DEV_STOP_TIMEOUT` (positive integer seconds, default 5). A timeout
returns nonzero and retains the ownership record rather than force-killing or
forgetting the group. Setup's unload warning belongs to the page, so Back and
reopening a skipped step do not clear it before one-time secrets are acknowledged.

The console suites are plain `node:test` files: `test:setup` covers the setup
state machine, `test:console` pins the mobile shell and setup-warning contracts
at source level (no browser harness exists here), and `test:routing` covers the
routing cycle check. `scripts/dev_test.sh` drives the process supervisor against
fake HTTP services on ephemeral ports, including the delayed-shutdown and
TERM-resistant cases.

Most tests are DB-free. `pkg/provider` tests cover the shared request/response tracing transport (metadata at info, redacted bodies at debug, no leaks of authorization headers or token fields). Winpay tests use `httptest` with generated RSA keys; the fake gateway checks signatures on outgoing calls. Midtrans BI-SNAP tests use `httptest` against a stub SNAP API that checks the B2B token and HMAC-SHA512 request signatures, covering create/inquiry/cancel for VA/QRIS/eWallet, token caching, RSA notification verification and status mapping. Midtrans legacy Core API tests use `httptest` against a stub `POST /v2/charge` server that checks the Basic Auth header, covering create/inquiry/cancel for VA (incl. Mandiri echannel)/QRIS/eWallet/retail outlet, the `SHA512(order_id + status_code + gross_amount + server_key)` notification signature, status_code-based error classification and status mapping. Xendit tests use `httptest` against a stub Payments API that checks Basic Auth and the `api-version` header, covering every supported method plus create/inquiry/cancel, `x-callback-token` verification and event/status mapping. DOKU tests use `httptest` against a stub SNAP API that checks the B2B token and HMAC-SHA512 request signatures, covering create/inquiry/cancel for VA/QRIS/eWallet, token caching, and notification signature verification. Live gateway sandbox testing requires valid credentials and current account-specific endpoint details; passing unit tests is not proof of live integration.

## 11. Current status and roadmap

Implemented: merchant API, core engine and routing (`priority`, `round_robin`), mock adapter, Winpay SNAP adapter (VA/Modern Retail/QRIS/eWallet), Midtrans legacy Core API adapter (VA/QRIS/eWallet/retail outlet), Midtrans BI-SNAP adapter (VA/QRIS/eWallet), Xendit Payments API v3 adapter (VA/QRIS/eWallet/retail outlet/pay later/card), DOKU SNAP adapter (VA/QRIS/eWallet), provider API request/response tracing, provider registry/contract, inbound webhooks, signed/retried outbound webhooks, PostgreSQL persistence/migrations, CLI, and tests.

Known/planned: persist Winpay/Midtrans BI-SNAP/DOKU gateway correlation values (the legacy Midtrans Core API adapter needs none); live sandbox verification; additional gateway adapters; first-class refunds; production queue/metrics/dashboards (provider-request tracing already ships); stronger recovery for provider success followed by DB failure; email flows for console accounts (password reset/invite). Do not claim roadmap items are implemented.

Also implemented since the original scope: the admin API (`/admin/api` on the API server, server-side sessions with a setup-wizard bootstrap, RBAC owner > operator > tech enforced per route, provider credential CRUD stored in `provider_configs` with hot reload into the running registry, inbound reprocess, outbound retry, append-only audit log, `migrations/0002_admin.sql`) and the admin console (`admin/`, Next.js) wired to it over a same-origin proxy, every screen reads real data; unwired features (refunds, transaction event log, provider debug log) are flag-gated off rather than shown. One-time secrets (merchant webhook secret, API key, invited user password) are revealed once and stored hashed. The routing page hosts the visual failover-chain editor (wired, see the admin section above); the compact priority list editor remains on the merchant detail page.

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
