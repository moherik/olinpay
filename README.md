# openpay.id

Open source **payment orchestration** for Indonesia.

`openpay.id` sits between your business system and the many payment gateways
(PJP) in Indonesia — Midtrans, Xendit, Duitku, OY!, and so on. Instead of
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
├── cmd/openpay/            # entrypoint: serve | migrate | seed
├── internal/
│   ├── api/                # API interface (chi handlers, auth, error mapping)
│   ├── config/             # environment configuration loader
│   ├── core/               # core engine + routing strategies
│   ├── domain/             # transaction, merchant, webhook, errors
│   ├── platform/           # logger, id, httpx, migrate (no business logic)
│   ├── repository/         # persistence interfaces
│   │   └── postgres/       # pgx implementations + all SQL
│   ├── testutil/memory/    # in-memory repos for tests
│   └── webhook/            # webhook engine: receiver + dispatcher
├── migrations/             # SQL migrations (applied by `openpay migrate`)
└── pkg/provider/           # PUBLIC contract every provider implements
    ├── mock/               # reference provider (in-memory gateway)
    └── winpay/             # real-world example: Winpay SNAP API adapter
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
make seed

# 5. run the servers (API on :8080, webhook engine on :8081)
make run
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

## API reference

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
package midtrans

import (
    "context"
    "net/http"

    "github.com/moherik/openpay.id/pkg/provider"
)

const Name = "midtrans"

type Provider struct {
    serverKey string
    baseURL   string
    client    *http.Client
}

func New(cfg provider.Config) (*Provider, error) {
    key, err := cfg.Required("server_key")
    if err != nil {
        return nil, err
    }
    return &Provider{
        serverKey: key,
        baseURL:   cfg.Get("base_url", "https://api.midtrans.com"),
        client:    &http.Client{Timeout: 30 * time.Second},
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

Then enable it:

```bash
# .env
OPENPAY_PROVIDERS=mock,midtrans
OPENPAY_PROVIDER_MIDTRANS_SERVER_KEY=SB-Mid-server-xxxx
```

Import the package for its side effects in `cmd/openpay/main.go` (or let it be
discovered by the host application):

```go
import _ "github.com/moherik/openpay.id/pkg/provider/midtrans"
```

### Rules for a good adapter

1. Map every gateway status onto exactly one `provider.Status`.
2. Return the gateway's own transaction id in `ProviderReference`; the engine
   uses it to correlate callbacks.
3. Treat `ReferenceID` as an idempotency key.
4. Keep `ParseWebhook` pure — no I/O, no side effects.
5. Wrap failures in `*provider.Error` so the core engine can react.

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

It supports Virtual Accounts, QRIS and eWallets. Enable it with:

```bash
# .env
OPENPAY_PROVIDERS=mock,winpay
OPENPAY_PROVIDER_WINPAY_PARTNER_ID=962489e9-de5d-4eb7-92a4-b07d44d64bf4
OPENPAY_PROVIDER_WINPAY_PRIVATE_KEY=MIIEowIBAAKCAQB5jAWUddv1tZW6...   # PEM or base64 DER
OPENPAY_PROVIDER_WINPAY_PUBLIC_KEY=MIGeMA0GCSqGSIb3DQEBAQUAA4GMADCBiAKBgHGK...
OPENPAY_PROVIDER_WINPAY_BASE_URL=https://sandbox-snap.winpay.id
OPENPAY_PROVIDER_WINPAY_CALLBACK_URL=https://openpay.example/webhooks/winpay
```

Point Winpay's merchant callback at `POST /webhooks/winpay`. Leave
`PUBLIC_KEY` empty to skip signature verification (development only).

## Routing

Each merchant carries a routing configuration (stored as JSONB):

```json
{
  "strategy": "priority",
  "providers": [
    {"name": "midtrans", "priority": 1, "enabled": true},
    {"name": "xendit",   "priority": 2, "enabled": true}
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
| `OPENPAY_PROVIDERS` | – | Comma separated provider names to enable |
| `OPENPAY_PROVIDER_<NAME>_<KEY>` | – | Per-provider config, e.g. `OPENPAY_PROVIDER_MOCK_SECRET` |
| `OPENPAY_API_KEY_PEPPER` | – | Secret mixed into API-key hashing |
| `OPENPAY_WEBHOOK_MAX_ATTEMPTS` | `6` | Outbound delivery attempts |

CLI: `openpay serve`, `openpay migrate`, `openpay seed`, `openpay version`.

## Testing

```bash
make test        # go test ./...
make test-race   # with the race detector
make check       # fmt + vet + test
```

Tests run without PostgreSQL by using the in-memory repositories in
`internal/testutil/memory`. The suite covers the provider lifecycle and
signature verification, routing strategies, the core engine (idempotency,
webhook reconciliation, cancel), the API surface, and the inbound webhook
engine.

The Winpay adapter has its own suite (`pkg/provider/winpay`) that runs against a
stub SNAP server which validates the RSA signature on every outgoing request, so
the mapping, signing and callback-parsing logic are exercised end to end.

## Status / roadmap

Implemented: the full skeleton — API interface, core engine with routing, the
provider contract + registry, the mock provider, inbound webhook receiver,
outbound dispatcher with retries, PostgreSQL persistence, migrations, and tests.

Also implemented: a complete **Winpay (SNAP API)** adapter as a worked example of
a real gateway integration (VA, QRIS, eWallet).

Deliberately left for later:

- further concrete gateway adapters (Midtrans, Xendit, Duitku, OY!, ...) —
  the interface is ready, see [Adding a provider](#adding-a-provider);
- persisting the gateway-specific correlation identifiers the Winpay adapter
  currently caches in memory (contractId, VA number, channel);
- refunds as a first-class operation (the pattern is the optional
  `SignatureVerifier`-style extension interface);
- an admin API for merchants and routing configuration (today: `openpay seed`);
- metrics/tracing, rate limiting, and a message-queue backend for the
  dispatcher;
- amount-based / weighted / least-cost routing strategies.

## License

MIT — see [LICENSE](LICENSE).
