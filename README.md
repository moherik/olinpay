# openpay.id

Open source **payment orchestration** for Indonesia.

`openpay.id` sits between your business system and the many payment gateways
(PJP) in Indonesia — Midtrans, Xendit, DOKU, Duitku, OY!, and so on. Instead of
integrating each gateway one by one, you integrate **one API** and let the
orchestrator handle translation, routing, and callbacks.

```
        your business system
                 │  one unified API
                 ▼
        ┌──────────────────┐
        │  API INTERFACE   │   POST /v1/payments, GET /v1/payments/{id}, ...
        └────────┬─────────┘
                 ▼
        ┌──────────────────┐        ┌──────────────────┐
        │   CORE ENGINE    │◄──────►│  PROVIDER ENGINE │  one adapter per gateway
        │  routing + state │        └────────┬─────────┘
        └────────┬─────────┘                 │
                 │                           ▼
                 │                    ┌──────────────┐
                 │                    │  PJP / PG    │
                 │                    └──────┬───────┘
                 │                           │ callback
                 ▼                           ▼
        ┌──────────────────┐        ┌──────────────────┐
        │  WEBHOOK ENGINE  │◄───────│  WEBHOOK ENGINE  │  (inbound receiver)
        │  (outbound)      │        └──────────────────┘
        └────────┬─────────┘
                 │  signed webhook
                 ▼
        your business system
```

## Table of contents

- [Architecture](#architecture)
- [Project layout](#project-layout)
- [Quickstart](#quickstart)
- [API reference](#api-reference)
- [OpenAPI / Swagger](#openapi--swagger)
- [Admin console](#admin-console)
- [Adding a provider](#adding-a-provider)
- [Routing](#routing)
- [Webhooks to your system](#webhooks-to-your-system)
- [Configuration](#configuration)
- [Testing](#testing)
- [Status / roadmap](#status--roadmap)

## Architecture

Four cooperating components, all in one Go binary (`openpay`):

| Component | Package | Responsibility |
| --- | --- | --- |
| **API interface** | `internal/api` | Single HTTP surface for merchant systems: create payment, inquiry, cancel. Authenticates API keys, validates input, maps errors to HTTP. |
| **Core engine** | `internal/core` | The unified payment processor: translates requests, selects a provider via the routing strategy, flags transactions on status change, and emits outbound notifications. |
| **Provider engine** | `pkg/provider` + `pkg/provider/*` | One adapter per PJP. Translates unified requests into a gateway's own API calls and normalizes its payloads back. |
| **Webhook engine** | `internal/webhook` | Inbound: receives gateway callbacks, verifies signatures, asks the provider to parse them, and hands the result to the core engine. Outbound: delivers signed notifications to merchant systems with retries. |

### Flows

**Payment request**

```
your system → API interface → core engine → routing → provider engine → PJP
```

**Inquiry / expiry / cancel**

```
your system → API interface → core engine → provider engine → PJP
```

**Callback**

```
PJP → webhook engine (receiver) → provider engine (parse + verify)
    → core engine (correlate + flag transaction)
    → webhook engine (dispatcher) → your system
```

## Project layout

```
.
├── cmd/openpay/            # entrypoint: serve | migrate | seed | create-admin
├── internal/
│   ├── admin/              # admin API: auth/sessions, RBAC, console endpoints
│   ├── api/                # API interface (chi handlers, auth, error mapping)
│   ├── config/             # environment configuration loader
│   ├── core/               # core engine + routing strategies
│   ├── domain/             # transaction, merchant, webhook, errors
│   ├── platform/           # logger, id, httpx, migrate (no business logic)
│   ├── repository/         # persistence interfaces
│   │   └── postgres/       # pgx implementations + all SQL
│   ├── testutil/memory/    # in-memory repos for tests
│   └── webhook/            # webhook engine: receiver + dispatcher
├── admin/                  # Next.js operator console (see admin/README.md)
├── migrations/             # SQL migrations (applied by `openpay migrate`)
└── pkg/provider/           # PUBLIC contract every provider implements
    ├── mock/               # reference provider (in-memory gateway)
    ├── midtrans/           # real adapter: Midtrans legacy Core API (/v2/charge)
    ├── midtrans-snap/      # real adapter: Midtrans BI-SNAP Core API
    ├── winpay/             # real adapter: Winpay SNAP API
    ├── xendit/             # real adapter: Xendit Payments API v3
    └── doku/               # real adapter: DOKU SNAP API
```

`internal/` is private to this module. `pkg/provider` is the public SDK: anyone
writing a gateway adapter imports just that package.

## Quickstart

Requirements: Go 1.25+ and Docker (for PostgreSQL).

```bash
# 1. configuration
cp .env.example .env

# 2. start PostgreSQL
make up

# 3. apply migrations
make migrate

# 4. create a merchant; note the printed API key
#    (merchant IDs look like AW239823 — AW prefix, max 10 characters)
make seed

# 5. run the servers (API on :8080, webhook engine on :8081)
make run

# later, to abandon local data and return to the first-install wizard:
# make reset-onboarding          # destructive — asks you to type RESET
```

Create a payment:

```bash
curl -sS -X POST http://localhost:8080/v1/payments \
  -H "X-API-Key: <API_KEY_FROM_SEED>" \
  -H "Content-Type: application/json" \
  -d '{
        "reference_id": "INV-0001",
        "amount": 150000,
        "method": "virtual_account",
        "channel": "MOCK_VA",
        "customer": {"name": "Budi", "email": "budi@example.com"},
        "description": "Order #INV-0001"
      }' | jq
```

Simulate the gateway callback (the mock gateway verifies HMAC signatures, so
sign the body with the provider secret):

```bash
BODY='{"reference_id":"INV-0001","status":"paid","amount":150000}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "mock-secret" | awk '{print $2}')

curl -sS -X POST http://localhost:8081/webhooks/mock \
  -H "Content-Type: application/json" \
  -H "X-Mock-Signature: $SIG" \
  -d "$BODY" | jq
```

The transaction is now `paid`, and a signed `payment.paid` webhook has been
queued for delivery to the merchant's callback URL.

## Admin console

An operator dashboard lives in [`admin/`](admin/README.md) (Next.js). It
mounts nothing itself: the Go API server serves the admin API under
`/admin/api`, and the console proxies to it so the session cookie stays
same-origin and HttpOnly.

```bash
# console on :3000 — backend must be running
cd admin && npm install && npm run dev
# first install: the console walks through owner account, first gateway and
# first merchant — or create a bootstrap account from the CLI:
openpay create-admin --email owner@example.com --role owner
```

On the very first open, `/setup` hosts the **first-install wizard**: while no
console account exists, `GET /admin/api/console-status` reports
`needs_setup: true`, the login page links to the wizard, and
`POST /admin/api/setup` creates the owner (name, email, password ≥10 chars)
and signs the browser in immediately — the wizard then offers three one-time
shortcuts over the normal authenticated API: first gateway credentials
(`PUT /providers/{name}/config`, adapter-validated), and the first merchant
plus its sandbox API key (both one-time secrets shown once). Every wizard
step after the account is skippable. Once any account exists the setup
endpoint answers `409 setup_completed` — `openpay create-admin` remains the
equivalent CLI path and starts refusing too.

It covers transaction search and cancel, merchant lifecycle and routing,
API keys, provider credentials (stored in PostgreSQL, hot-reloaded into the
running server), inbound reprocessing, outbound retries, users and an
append-only audit log — all gated by the `owner` > `operator` > `tech` role
hierarchy the backend enforces per route.

## API reference

The machine-readable API specification is available at
[`docs/openapi.yaml`](docs/openapi.yaml) (OpenAPI 3.1). Open it in Swagger UI,
Swagger Editor, or another OpenAPI-compatible tool. The merchant API and
gateway webhook server use separate server URLs in the document.

When the service is running, open [`http://localhost:8080/docs`](http://localhost:8080/docs)
for the interactive Swagger UI, or fetch the embedded spec from
[`http://localhost:8080/openapi.yaml`](http://localhost:8080/openapi.yaml).
The Swagger UI assets are loaded from a CDN, so the browser needs internet
access. Click **Authorize** and enter the seeded merchant API key, then use
**Try it out**. The service itself does not need internet access to serve the
embedded specification.

All API endpoints require authentication via `X-API-Key: <key>` or
`Authorization: Bearer <key>`.

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/payments` | Create a payment. Idempotent on `reference_id`. |
| `GET` | `/v1/payments/{id}` | Fetch a payment. Add `?sync=true` to refresh from the gateway. |
| `GET` | `/v1/payments` | List payments (`reference_id`, `status`, `provider`, `method`, `from`, `to`, `limit`, `offset`). |
| `POST` | `/v1/payments/{id}/cancel` | Cancel or expire a pending payment. Body: `{"reason":"cancel"}`. |
| `GET` | `/v1/providers` | Discover registered providers and their capabilities. |
| `GET` | `/v1/me` | Authenticated merchant profile. |
| `GET` | `/healthz`, `/readyz` | Liveness / readiness (public). |

Webhook engine (gateway-facing, no API key — authenticated per provider):

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/webhooks/{provider}` | Inbound gateway callback. |
| `GET` | `/healthz` | Liveness (public). |

### Create payment body

```jsonc
{
  "reference_id": "INV-0001",     // required, unique per merchant (idempotency key)
  "amount": 150000,               // required, smallest unit (rupiah for IDR)
  "currency": "IDR",              // optional, defaults to IDR
  "method": "virtual_account",    // required, one of the unified methods
  "channel": "BCA",               // optional, gateway-specific instrument
  "provider": "midtrans",         // optional, forces a provider (skips routing)
  "description": "Order #1",      // optional
  "expiry_seconds": 86400,        // optional
  "customer": {"name": "Budi"},   // optional
  "items": [{"name": "Item", "price": 150000, "quantity": 1}],
  "metadata": {"order_ref": "OMS-1"}
}
```

Unified methods: `virtual_account`, `qris`, `ewallet`, `retail_outlet`, `card`,
`paylater`, `direct_debit`.

Unified statuses: `pending`, `paid`, `expired`, `cancelled`, `failed`,
`refunded`, `unknown`.

## Adding a provider

Every gateway adapter implements **one interface** — `provider.Provider` in
`pkg/provider` — with exactly four operations:

```go
type Provider interface {
    Name() string
    Capabilities() Capabilities
    CreatePayment(ctx context.Context, req *CreatePaymentRequest) (*CreatePaymentResponse, error)
    Inquiry(ctx context.Context, req *InquiryRequest) (*InquiryResponse, error)
    Cancel(ctx context.Context, req *CancelRequest) (*CancelResponse, error)
    ParseWebhook(ctx context.Context, req *WebhookRequest) (*WebhookEvent, error)
}
```

Optionally implement:

- `provider.SignatureVerifier` — to authenticate inbound callbacks (the webhook
  engine calls it before parsing).
- `provider.WebhookResponder` — to control the exact acknowledgement body sent
  back to the gateway (some gateways retry until they see their own format).
- `provider.HealthChecker` — to report readiness.

A provider never touches the database or the core engine; it only translates.
`pkg/provider/mock` is a complete, working reference implementation.

### Skeleton

```go
package acmegateway

import (
    "context"
    "net/http"
    "time"

    "github.com/moherik/openpay.id/pkg/provider"
)

const Name = "acmegateway"

type Provider struct {
    apiKey  string
    baseURL string
    client  *http.Client
}

func New(cfg provider.Config) (*Provider, error) {
    key, err := cfg.Required("api_key")
    if err != nil {
        return nil, err
    }
    return &Provider{
        apiKey:  key,
        baseURL: cfg.Get("base_url", "https://api.acmegateway.example"),
        // Traces every request/response through the host logger.
        client: provider.NewHTTPClient(Name, 30*time.Second),
    }, nil
}

// Register the factory so the host application can build it from config.
func init() {
    provider.Register(Name, func(cfg provider.Config) (provider.Provider, error) {
        return New(cfg)
    })
}

func (p *Provider) Name() string { return Name }

func (p *Provider) Capabilities() provider.Capabilities {
    return provider.Capabilities{
        Methods:         []provider.Method{provider.MethodVirtualAccount, provider.MethodQRIS},
        SupportsInquiry: true,
        SupportsCancel:  true,
        SupportsWebhook: true,
        SupportsSandbox: true,
    }
}

func (p *Provider) CreatePayment(ctx context.Context, req *provider.CreatePaymentRequest) (*provider.CreatePaymentResponse, error) {
    // 1. map req (ReferenceID, Amount, Method, Channel, Customer) onto the
    //    gateway's payload.
    // 2. call the gateway.
    // 3. map the gateway response back into CreatePaymentResponse, translating
    //    the gateway status onto a provider.Status.
    // 4. wrap gateway failures with provider.NewError / provider.WrapError.
    return nil, nil
}

func (p *Provider) Inquiry(ctx context.Context, req *provider.InquiryRequest) (*provider.InquiryResponse, error) { ... }
func (p *Provider) Cancel(ctx context.Context, req *provider.CancelRequest) (*provider.CancelResponse, error)   { ... }
func (p *Provider) ParseWebhook(ctx context.Context, req *provider.WebhookRequest) (*provider.WebhookEvent, error) { ... }
```

Then enable it (the console's Providers page, or `OPENPAY_PROVIDERS` plus the
`OPENPAY_PROVIDER_<NAME>_<KEY>` env vars):

```bash
# .env
OPENPAY_PROVIDERS=mock,acmegateway
OPENPAY_PROVIDER_ACMEGATEWAY_API_KEY=sk_live_xxxx
```

Import the package for its side effects in `cmd/openpay/main.go` (or let it be
discovered by the host application):

```go
import _ "github.com/moherik/openpay.id/pkg/provider/acmegateway"
```

### Rules for a good adapter

1. Map every gateway status onto exactly one `provider.Status`.
2. Return the gateway's own transaction id in `ProviderReference`; the engine
   uses it to correlate callbacks.
3. Treat `ReferenceID` as an idempotency key.
4. Keep `ParseWebhook` pure — no I/O, no side effects.
5. Wrap failures in `*provider.Error` so the core engine can react.
6. Build the HTTP client with `provider.NewHTTPClient(Name, timeout)` so every
   gateway request and response is traced (with credentials redacted) through the
   application logger.

### A real-world example: Winpay

`pkg/provider/winpay` is a full adapter for [Winpay](https://docs.winpay.id),
an Indonesian PJP that exposes Bank Indonesia's **SNAP API**. It is the best
place to look when writing your own adapter, because it demonstrates the parts
the mock provider fakes:

- **Request signing** — the SNAP "Asymmetric Without Get Token" scheme signs
  `HTTPMethod:EndpointUrl:Lowercase(HexEncode(SHA-256(minify(body)))):Timestamp`
  with RSA-SHA256 and sends it as `X-SIGNATURE`.
- **Callback verification** — the same formula is verified with Winpay's public
  key. This is why `provider.WebhookRequest` carries the callback `Path`.
- **Custom acknowledgement** — `WebhookResponder` replies with the SNAP
  `{"responseCode":"2002500",...}` body so Winpay does not retry.
- **Gateway-specific correlation** — Winpay addresses inquiry/cancel by the
  `contractId` (and a VA number) it returned at creation; the adapter keeps a
  short-lived cache of those, since the unified contract does not carry them.

It supports Virtual Accounts, QRIS and eWallets. Enable it from the console's
Providers page: save `partner_id` and `private_key` for the sandbox
environment, plus `public_key`, `base_url` and `callback_url` as needed. The
credentials are stored in the database and hot-reload without a restart. The
`OPENPAY_PROVIDER_WINPAY_*` environment variables carry the same keys for a
bare deployment.

Point Winpay's merchant callback at `POST /webhooks/winpay`. Leave
`PUBLIC_KEY` empty to skip signature verification (development only).

### A real-world example: Midtrans (legacy Core API)

`pkg/provider/midtrans` is a full adapter for [Midtrans'](https://docs.midtrans.com/reference/core-api-overview)
**legacy Core API**, the REST product built around `POST /v2/charge` and
addressed by `order_id`. It charges a specific instrument directly rather than
hosting a checkout page, and is distinct from both the hosted Snap product and
the BI-SNAP API below. It supports:

- **Virtual Account** — bank transfer for PERMATA, BCA, BNI, BRI, CIMB, DANAMON,
  BSI, SEABANK and SAQU, plus **Mandiri Bill Payment** (`echannel`, channel code
  `MANDIRI`) returned as a biller code + bill key.
- **QRIS** — dynamic merchant-presented QR (`channel` `QRIS`).
- **eWallet** — GoPay (`GOPAY`) and ShopeePay (`SHOPEEPAY`), returned as
  deeplink/redirect actions.
- **Retail outlet** — Indomaret (`INDOMARET`) and Alfamart (`ALFAMART`).

It demonstrates the parts a real Core API integration needs:

- **Basic Auth** — `Authorization: Basic base64("<server_key>:")`: the server key
  as the username with an empty password.
- **Callback authentication** — Midtrans signs notifications with
  `SHA512(order_id + status_code + gross_amount + server_key)`; the adapter
  recomputes the digest in constant time and rejects callbacks when `server_key`
  is not configured.
- **Status mapping** — `capture`/`settlement` (paid), `pending`/`authorize`
  (pending), `deny`/`failure` (failed), `cancel`, `expire` and the refund family
  are normalized onto the unified statuses, accounting for `fraud_status`.
- **No correlation cache** — the Core API status/cancel/expire endpoints are
  addressed by the `order_id` (the merchant `reference_id`), so Inquiry and
  Cancel resolve from the reference alone.
- **Ambiguous outcome handling** — Midtrans reports business errors with HTTP 200
  and a non-2xx `status_code` (for example `406` for a duplicate order id), so
  the adapter inspects the body `status_code` rather than the transport status.

Enable it from the console's Providers page by saving `server_key` (plus
`base_url`, `qris_acquirer` or callback URLs as needed) per environment; the
`OPENPAY_PROVIDER_MIDTRANS_*` environment variables remain available for a
bare deployment.

Point Midtrans' notification URL at `POST /webhooks/midtrans`. Per-payment
`metadata` can carry `va_number` (VA only), `return_url` (e-wallet only) and
`qris_acquirer` (QRIS acquirer override for one payment). Use
`https://api.midtrans.com` as `BASE_URL` in production.

### A real-world example: Midtrans (BI-SNAP Core API)

`pkg/provider/midtrans-snap` is a full adapter for [Midtrans](https://docs.midtrans.com/reference/core-api-snap-open-api-overview)
built on Midtrans' **BI-SNAP Core API** (Bank Indonesia's *Standar Nasional Open
API Pembayaran*), which charges a specific instrument directly rather than
hosting a checkout page. It is registered as `midtrans-snap` so it can coexist
with the legacy Core API adapter above. It supports:

- **Virtual Account** — bank transfer for PERMATA, BCA, MANDIRI, CIMB, BNI, BRI,
  DANAMON, BSI, SEABANK and SAQU (channel codes are the bank names).
- **QRIS** — dynamic merchant-presented QR (`channel` `QRIS`).
- **eWallet** — GoPay (`GOPAY`), ShopeePay (`SHOPEEPAY`) and DANA (`DANA`) via the
  direct-debit host-to-host flow, returned as deeplink/redirect actions.

It demonstrates the parts a real BI-SNAP integration needs:

- **Two signature schemes** — a B2B access token signed with RSA-SHA256 over
  `clientID|timestamp`, then an HMAC-SHA512 `X-SIGNATURE` over
  `HTTPMethod:EndpointUrl:AccessToken:SHA-256(body):X-TIMESTAMP` on every
  transactional call. The token is cached and refreshed on expiry or a 401.
- **Callback authentication** — Midtrans signs notifications with its own RSA
  key; the `X-SIGNATURE` header is verified with the configured `public_key`.
- **Status mapping** — the SNAP numeric status (`00` paid, `03` pending, `05`
  cancelled, `06` failed, `08` expired, ...) is normalized onto the unified
  statuses.
- **An in-process correlation cache** — Midtrans' status/cancel endpoints are
  addressed by gateway identifiers (the VA number, the original reference
  numbers, the original `X-EXTERNAL-ID`) that the unified contract does not carry
  between calls, so `CreatePayment` caches them. A restart loses the cache; a
  production deployment would persist these identifiers alongside the
  transaction.

Enable it from the console's Providers page by saving the credentials per
environment: `client_id`, `client_secret` and `private_key` are required;
`public_key`, `partner_id`, `merchant_id`, `partner_service_id` and `base_url`
are per-gateway additions. The `OPENPAY_PROVIDER_MIDTRANS_SNAP_*` environment
variables remain available for a bare deployment.

Point Midtrans' notification URL at `POST /webhooks/midtrans-snap`. Two values
are passed per payment through `metadata`:

- `return_url` — e-wallet only: the browser URL the payer returns to after
  paying (Midtrans `urlParams` `PAY_RETURN`). The dashboard `PAY_RETURN` url is
  used when it is omitted.
- `sub_merchant_id` — QRIS only: the merchant id that is unique per
  (sub)merchant and addresses the QRIS transaction; the configured `MERCHANT_ID`
  is the fallback when it is omitted.

Use `https://merchants.midtrans.com` as `BASE_URL` in production.

Capabilities differ from the legacy Core API: BI-SNAP supports only virtual
account, QRIS and e-wallet, so retail outlet, pay later and card are **not**
available. Channel availability is account-specific and has not been verified
against the live sandbox.

### A real-world example: Xendit

`pkg/provider/xendit` is a full adapter for [Xendit](https://docs.xendit.co/apidocs/create-payment-request)
built on the **Payments API v3** (`POST /v3/payment_requests`), a single payment
request endpoint that selects a concrete instrument with a `channel_code`. It
supports:

- **Virtual Account** — `BCA_VIRTUAL_ACCOUNT`, `BNI_VIRTUAL_ACCOUNT`,
  `BRI_VIRTUAL_ACCOUNT`, `MANDIRI_VIRTUAL_ACCOUNT`, `PERMATA_VIRTUAL_ACCOUNT`,
  `CIMB_VIRTUAL_ACCOUNT`, `BSI_VIRTUAL_ACCOUNT`, `BNC_VIRTUAL_ACCOUNT`,
  `MUAMALAT_VIRTUAL_ACCOUNT` and `BJB_VIRTUAL_ACCOUNT`.
- **QRIS** — the dynamic merchant-presented QR (`channel_code QRIS`), returned as
  a `QR_STRING` action and as the payment code.
- **eWallet** — OVO, DANA, ShopeePay, LinkAja, JeniusPay, AstraPay, GoPay and
  Nexcash, returned as redirect/deeplink actions.
- **Retail outlet** — Alfamart and Indomaret (reusable payment code).
- **Pay later** — Akulaku, Kredivo, Atome and Indodana.
- **Card** — via a saved Xendit payment token, passed in
  `metadata.payment_token_id` (or `metadata.card_token`).

It demonstrates the parts a real Payments API v3 integration needs:

- **Basic Auth** — `Authorization: Basic base64("<secret_key>:")`, plus the
  `api-version` header (`2024-11-11` by default) on every call.
- **Callback authentication** — the shared `x-callback-token` header is compared
  in constant time against `CALLBACK_TOKEN`; callbacks are rejected when the
  token is not configured.
- **Status mapping** — the payment request status (`REQUIRES_ACTION`,
  `SUCCEEDED`, `FAILED`, `EXPIRED`, `CANCELED`, ...) and the payment status are
  normalized onto the unified statuses; refund events are mapped by name.
- **Idempotency without a cache** — Xendit keys a payment request by its own
  `payment_request_id`, returned as `provider_reference`, so Inquiry and Cancel
  address the request directly.
- **Escape hatch** — channel-specific fields (for example an OVO account number)
  can be passed in `metadata.channel_properties` as a JSON object.

Enable it from the console's Providers page by saving `secret_key` (and
`callback_token`, so inbound callbacks are verified) per environment. The
`OPENPAY_PROVIDER_XENDIT_*` environment variables remain available for a bare
deployment.

Point Xendit's webhook URL at `POST /webhooks/xendit` and set the same
verification token as `CALLBACK_TOKEN`. Test and live mode share the
`https://api.xendit.co` host; select one with the key (`xnd_development_...` vs
`xnd_production_...`). The v3 API exposes no forced-expiry operation, so a
`Cancel` with reason `expire` is performed as a cancel.

### A real-world example: DOKU

`pkg/provider/doku` is a full adapter for [DOKU](https://developers.doku.com),
an Indonesian PJP that exposes Bank Indonesia's **SNAP API**. It supports:

- **Virtual Account** — one-off (C), open (O) and bill-variable (V) VAs across
  BCA, BRI, BNI, BNC, Mandiri, Permata, CIMB, BSI, Danamon, Maybank, Sinarmas,
  BTN and BJB. Each bank has its own create-va path and channel enum.
- **QRIS** — merchant-presented QR via the snap-adapter endpoints.
- **eWallet** — DANA and ShopeePay via the direct-debit host-to-host flow.

It demonstrates the parts a real SNAP "Symmetric Signature with Get Token"
integration needs:

- **Two-step auth** — a B2B access token is fetched from
  `/authorization/v1/access-token/b2b`, signing `clientID|timestamp` with
  RSA-SHA256; every transactional request then carries an HMAC-SHA512 signature
  (keyed by `client_secret`) over
  `HTTPMethod:EndpointUrl:AccessToken:Lowercase(HexEncode(SHA-256(minify(body)))):Timestamp`
  plus an `Authorization: Bearer <token>` header. The token is cached and
  refreshed near its 900s expiry.
- **Callback verification** — outbound notifications are HMAC-SHA512 signed with
  the merchant's `client_secret`; the adapter verifies them over the callback
  path, trying each candidate access token.
- **Custom acknowledgement** — `WebhookResponder` replies with the SNAP
  `{"responseCode":"2002500",...}` body so DOKU does not retry.
- **Gateway-specific correlation** — DOKU addresses status/delete by the VA
  number and original reference numbers it returned at creation; the adapter
  keeps a short-lived cache of those, since the unified contract does not carry
  them.

Enable it from the console's Providers page by saving `client_id`,
`client_secret`, `private_key` and `partner_service_id` per environment
(`merchant_id`, `terminal_id` and `postal_code` matter for QRIS). The
`OPENPAY_PROVIDER_DOKU_*` environment variables remain available for a bare
deployment.

Point DOKU's **Notification URL** at `POST /webhooks/doku`. Use
`https://api.doku.com` as `BASE_URL` in production. DOKU does not expose a
cancel endpoint for e-wallet host-to-host payments, so `Cancel` returns
`ErrUnsupportedOperation` for that method.

## Routing

Each merchant carries a routing configuration (stored as JSONB):

```json
{
  "strategy": "priority",
  "providers": [
    {"name": "midtrans",      "priority": 1, "enabled": true},
    {"name": "midtrans-snap", "priority": 2, "enabled": true},
    {"name": "xendit",        "priority": 3, "enabled": true}
  ]
}
```

Built-in strategies (`internal/core/routing.go`):

- **`priority`** — order by ascending priority.
- **`round_robin`** — rotate the starting provider per merchant/method.

The engine treats the ordered result as a failover chain: if the first provider
errors on create, the next is tried. Add your own strategy by implementing
`core.Strategy` and registering it on the router.

A merchant with an empty provider list routes to **every** registered provider
that supports the requested method — convenient for local development.

### Conditional routes

A merchant can also configure conditional rules that steer specific requests
before the fallback chain. The first rule whose condition matches prepends its
provider chain ahead of the configured fallback chain, so a failed rule route
still falls through the base chain on a hard decline:

```json
{
  "strategy": "priority",
  "providers": [
    {"name": "mock", "priority": 1, "enabled": true}
  ],
  "enrich": [
    {"op": "set_metadata", "key": "source", "value": "openpay"}
  ],
  "rules": [
    {
      "condition": {"field": "metadata:acquirer", "operator": "eq", "value": "winpay"},
      "apply": [
        {"op": "set_metadata", "key": "routed_via", "value": "rule-winpay"}
      ],
      "providers": ["winpay.sandbox"]
    },
    {
      "condition": {"field": "amount", "operator": "gte", "value": "1000000"},
      "reject": {"status": 402, "code": "amount_not_supported", "message": "Amount too large for this merchant."}
    }
  ],
  "reject": {"status": 503, "code": "gateways_down", "message": "All gateways declined the payment."}
}
```

Condition fields: `amount` (compares the request's integer `amount` — integer
rupiah for IDR per the create-payment contract; `gte`/`lte`/`eq`/`neq`),
`method`, `channel`, `currency`, and `metadata:<key>` (compares a `metadata`
entry from the request — a rule never fires on requests missing that key).
Operators: `eq`, `neq`, and `gte`/`lte` on `amount`.

#### Request edits (`enrich`, `apply`)

Modify operations edit the payment request before it reaches a condition or a
gateway:

- `enrich` entries run **before rule evaluation**, so conditions can read the
  enriched values. Config-level edits apply to every payment.
- `apply` entries inside a rule run **when that rule matches**, before its
  provider chain is called.
- Ops: `set_metadata` (`key` + `value`) and `set_description` (`value`). Edits
  apply in list order to the transaction and the provider request alike.

#### Custom responses (`reject`)

A reject answers the payment API with the merchant's own identity instead of a
gateway error. Three places:

- **rule `reject`** — the first matching rule with a `reject` terminates
  routing immediately; the payment is answered without touching any provider.
- **config-level `reject`** — when every provider in the chain fails to create
  the payment, the configured response answers instead of the raw gateway
  error.
- The payment API returns the stored `status` with the standard error envelope
  `{"error": {"code": ..., "message": ...}}`. A stored reject must be a 4xx/5xx
  (validation refuses the rest on save, and the API clamps stray values into
  the error range so no response can ever ship a 2xx/3xx body for an
  unaccepted payment). Admin operations surface a routing rejection as
  `409 routing_rejected`.

The admin API validates the whole configuration on save (known field, allowed
operator, integer amounts, live provider instances, modify op/key completeness,
reject status/code/message, and reject-rules may not also route to providers).
The console's routing page edits it all in the **visual graph**: modify nodes
directly after the entry enrich every request; condition nodes branch to a
match branch of modify nodes, a provider chain, or a respond node; the
no-match branch continues to the next condition or the fallback chain, which
may end in a respond node for payments the whole chain declines. Conditions
must sit before the fallback providers — rules are evaluated before the
fallback chain, so the visual order matches runtime order.

### Environments and forced providers

Every request runs in one of two environments (sandbox or production, derived
from the API key). Provider candidates are scoped to the requesting
environment: DB-saved provider instances are registered as
`<adapter>.<environment>` (e.g. `winpay.sandbox`) and never serve the other
environment; environment-variable configured instances keep their bare adapter
name (e.g. `mock`) and serve both.

The optional `"provider"` field in a create-payment request also accepts the
bare adapter name: the engine resolves it to the instance registered for the
request's environment (`"winpay"` + sandbox → `winpay.sandbox`). An exact
instance name keeps working, and forcing a cross-environment instance is
rejected with `no_route`.

## Webhooks to your system

When a transaction changes status, the engine enqueues a notification that the
dispatcher delivers (and retries with exponential backoff). Delivery is
persisted first, so a crash never loses an event.

Request:

```
POST <your callback_url>
Content-Type: application/json
X-OpenPay-Event: payment.paid
X-OpenPay-Delivery: whd_...
X-OpenPay-Timestamp: 1730000000
X-OpenPay-Signature: <hex HMAC-SHA256 of "<timestamp>.<body>">
```

Body:

```json
{
  "id": "whd_...",
  "event": "payment.paid",
  "created_at": "2026-01-01T00:00:00Z",
  "data": { "id": "trx_...", "status": "paid", "amount": 150000, "...": "..." }
}
```

Verify with your merchant webhook secret:

```go
mac := hmac.New(sha256.New, []byte(webhookSecret))
mac.Write([]byte(timestamp))
mac.Write([]byte("."))
mac.Write(body)
valid := hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature))
```

Return any `2xx` to acknowledge. `id` is stable across retries, so use it as an
idempotency key. Events: `payment.pending`, `payment.paid`, `payment.expired`,
`payment.cancelled`, `payment.failed`, `payment.refunded`.

## Configuration

See [`.env.example`](.env.example) for the full list. The essentials:

| Variable | Default | Description |
| --- | --- | --- |
| `OPENPAY_DATABASE_URL` | local postgres | PostgreSQL DSN |
| `OPENPAY_API_ADDR` | `:8080` | API interface listen address |
| `OPENPAY_WEBHOOK_ADDR` | `:8081` | Webhook engine listen address |
| `OPENPAY_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `OPENPAY_PROVIDERS` | – | Optional; comma separated provider names to bootstrap from the environment (console-stored instances boot without it) |
| `OPENPAY_PROVIDER_<NAME>_<KEY>` | – | Per-provider config for env-built instances, e.g. `OPENPAY_PROVIDER_WINPAY_PARTNER_ID` |
| `OPENPAY_API_KEY_PEPPER` | – | Secret mixed into API-key hashing |
| `OPENPAY_WEBHOOK_MAX_ATTEMPTS` | `6` | Outbound delivery attempts |

CLI: `openpay serve`, `openpay migrate`, `openpay seed`, `openpay create-admin`,
`openpay version`.

Provider credentials set through the console take priority over the
`OPENPAY_PROVIDER_*` environment variables: a DB row for the same
adapter/environment replaces the env-built instance at save time (and when the
process restarts). Env-configured instances remain the fallback so a bare
`OPENPAY_PROVIDERS=xendit` deployment keeps working as before. With no
`OPENPAY_PROVIDERS` at all the server boots purely from DB rows, and startup
fails only when neither source has produced a provider instance.

### Provider API logging

Every outbound call an adapter makes to a payment gateway is logged through the
JSON logger:

- At `info` (the default) each call emits one line with the provider, HTTP
  method, URL, response status and duration. Failed calls and non-2xx responses
  log at `warn`.
- At `debug` the request and response headers and bodies are added.

Credentials never reach the log: sensitive headers (`Authorization`,
`X-SIGNATURE`, client keys, cookies) and sensitive JSON fields (access tokens,
charge tokens, secrets, card data) are redacted as `***`, and bodies are capped
at 8 KiB. Tracing is installed automatically by the host binary; adapters opt in
by constructing their client with `provider.NewHTTPClient`.

## Testing

```bash
make test        # go test ./...
make test-race   # with the race detector
make check       # fmt + vet + test
```

Tests run without PostgreSQL by using the in-memory repositories in
`internal/testutil/memory`. The suite covers the provider lifecycle and
signature verification, routing strategies, the core engine (idempotency,
webhook reconciliation, cancel), the API surface, the inbound webhook engine,
and the admin API (session lifecycle, RBAC per route, provider config
save/delete with hot reload, audit writing).

The provider suites also cover the shared request/response tracing transport in
`pkg/provider` (metadata at info, redacted bodies at debug).

The Winpay adapter has its own suite (`pkg/provider/winpay`) that runs against a
stub SNAP server which validates the RSA signature on every outgoing request, so
the mapping, signing and callback-parsing logic are exercised end to end.

The Midtrans **BI-SNAP** adapter has a suite (`pkg/provider/midtrans-snap`)
against a stub SNAP server that verifies the RSA get-token signature and the
HMAC-SHA512 transaction signatures on every request and replays documented
response shapes, covering create/inquiry/cancel for VA/QRIS/e-wallet, token
caching, the RSA notification signature and the status mapping.

The Midtrans **legacy Core API** adapter has its own suite
(`pkg/provider/midtrans`) against a stub `POST /v2/charge` server that checks the
Basic Auth header on every request and replays documented response shapes,
covering create/inquiry/cancel for VA (including Mandiri `echannel`)/QRIS/
e-wallet/retail outlet, the `SHA512(order_id + status_code + gross_amount +
server_key)` callback signature, status-code-based error classification and the
status mapping.

The Xendit adapter has a suite (`pkg/provider/xendit`) against a stub Payments
API server that checks Basic Auth and the `api-version` header on every request
and replays documented request/response shapes, covering every supported method,
create/inquiry/cancel, the `x-callback-token` verification and the event/status
mapping.

## Status / roadmap

Implemented: the full skeleton — API interface, core engine with routing, the
provider contract + registry, the mock provider, inbound webhook receiver,
outbound dispatcher with retries, PostgreSQL persistence, migrations, and tests.

Also implemented: a complete **Winpay (SNAP API)** adapter as a worked example of
a real gateway integration (VA, QRIS, eWallet).

Also implemented: two **Midtrans** adapters — the **legacy Core API**
(`POST /v2/charge`: VA, QRIS, eWallet, retail outlet) and the **BI-SNAP Core
API** (VA, QRIS, eWallet) — each with its own callback signature verification.

Also implemented: a complete **Xendit (Payments API v3)** adapter (VA, QRIS,
eWallet, retail outlet, pay later, card) with callback token verification.

Also implemented: the **admin API** (`/admin/api` on the API server: server-side
sessions, RBAC, DB-backed provider credentials with hot reload, audit log) and
the **admin console** (Next.js, `admin/`) wired to it — including a routing
editor, a first-install setup wizard for brand-new installs
(`POST /api/setup`, self-disabling after the first owner account; the
`create-admin` CLI keeps working as the alternative bootstrap),
one-time secret reveals for merchant webhook secrets / API keys /
invited-user passwords, and flag-gated features.

Deliberately left for later:

- further concrete gateway adapters (Duitku, OY!, ...) —
  the interface is ready, see [Adding a provider](#adding-a-provider);
- persisting the gateway-specific correlation identifiers the Winpay, DOKU and
  Midtrans **BI-SNAP** adapters currently cache in memory (contractId, VA number,
  original reference numbers, `X-EXTERNAL-ID`); the legacy Midtrans Core API
  adapter needs no cache because it addresses by `order_id`;
- refunds as a first-class operation (the pattern is the optional
  `SignatureVerifier`-style extension interface);
- metrics/tracing, rate limiting, and a message-queue backend for the
  dispatcher;
- amount-based / weighted / least-cost routing strategies;
- forgot-password / email flows for console accounts (the only account
  creation paths are the first-install wizard and `create-admin`; nothing
  emails in this build).

## License

MIT — see [LICENSE](LICENSE).
