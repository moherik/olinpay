# Admin console

The operator dashboard for the AwsemPay platform (the `openpay.id` project),
built with Next.js. It talks to the Go
backend's admin API (`/admin/api` mount on the API server) — there is no demo
data: every number, list and credential on screen comes from the database.

## Run it

```bash
# backend on :8080 (see the repository README quickstart)
make run

# console on :3000
cd admin
npm install
npm run dev
```

`next.config.ts` proxies `/admin/api/*` to the backend (`OPENPAY_BACKEND_URL`,
default `http://localhost:8080`), so the session cookie is same-origin and
HttpOnly — no CORS and no token in JavaScript.

## Sign in

Sessions are server-side. On a brand-new install the console reports
"no account yet" on `/login` and links to the **first-install wizard** at
`/setup`: it creates the owner account (while no console account exists,
`POST /admin/api/setup` is open; after that it answers `409 setup_completed`)
and, signed in immediately, offers skippable one-time steps for the first
gateway credentials and the first merchant + API key.

Later console accounts are created by an authenticated owner through the Users
API. Sign in at `/login`; a session lasts 12 hours.

The first owner is created only through the browser wizard at `/setup`; there is
no CLI bootstrap path. Gateway setup is optional, so the backend can start with
no configured providers for first-install administration. Payment creation has
no route until an enabled provider is configured.

The wizard reveals each new merchant webhook secret and API key once. If merchant
creation succeeds but key creation cannot be confirmed, finish the wizard and
create or manage the merchant's keys manually from **API keys**.

## What it covers

- **Overview** — environment-scoped totals, hourly volume, delivery state and
  provider reliability.
- **Transactions** — server-side filtering (exact reference `q`, merchant,
  status, provider), cancel (RBAC-gated), per-transaction timeline including
  inbound gateway callbacks.
- **Merchants** — create/suspend/restore, callback URL, per-environment
  provider routing order, API keys (shown once at creation).
- **Webhooks** — outbound deliveries with retry, inbound callbacks with
  idempotent reprocess.
- **Providers** — gateway adapters with DB-stored credentials per environment;
  saving hot-reloads the instance into the running server.
- **Users & access** — `owner` > `operator` > `tech`; the backend enforces the
  role on every route and writes every console action to the audit log.

Features the backend does not implement yet (refunds, transaction event log,
provider debug logging) are flag-gated off rather than shown greyed out.

## Notes

- One-time secrets (merchant webhook secret, API key, invited user password)
  are displayed once and then only stored hashed.
- `make dev` starts the local backend and console together; use `make dev-stop`
  to stop the supervised process groups.
- The shell collapses to a labeled **Menu** on narrow screens: brand and Menu
  keep the first row, the environment and account controls reflow into a second
  one, and the account email appears only where the trigger expands.
- This version of Next.js uses Cache Components; the console is a
  client-rendered app behind an auth gate, see the source layout under
  `src/app/(console)/`.
