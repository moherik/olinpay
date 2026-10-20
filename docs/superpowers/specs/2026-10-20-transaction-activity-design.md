# Transaction Activity Timeline Design

## Intent

Make one transaction detail page answer the operational question: where did this payment stop, and what exact request or response proves it?

The page will show a compact transaction summary and a chronological activity stream covering the four boundaries:

1. Merchant system request to AwsemPay and AwsemPay response.
2. AwsemPay request to the selected provider and provider response.
3. Provider callback into AwsemPay.
4. AwsemPay webhook delivery to the merchant system.

## Goals

- Persist activity records so the timeline survives process restarts and can be inspected after a failure.
- Preserve redacted request/response headers and bodies, status codes, duration, errors, and timestamps.
- Correlate provider calls made before the transaction row exists by merchant, environment, and merchant reference; after creation, expose them on the transaction timeline.
- Include webhook attempts, including retries and failed responses, not just the final delivery state.
- Replace the oversized action/sidebar area with a compact summary/action strip and make activity the primary detail content.
- Keep loading, empty, error, keyboard, mobile, and light/dark states usable.

## Non-goals

- Do not store authorization credentials, signatures, cookies, API keys, webhook secrets, or private keys.
- Do not change payment routing, idempotency, callback verification, or webhook retry semantics.
- Do not promise byte-for-byte raw payload retention after redaction; preserve the redacted JSON/text representation.
- Do not expose activity from one merchant to another; every query remains transaction-scoped.

## Activity model

Add `domain.TransactionActivity` with:

- `ID`, `TransactionID` (nullable for pre-persistence provider attempts), `MerchantID`, `Environment`, `ReferenceID`.
- `Direction`: `merchant_request`, `merchant_response`, `provider_request`, `provider_response`, `provider_webhook`, `merchant_webhook`.
- `Phase`: `request`, `response`, or `event`.
- `Provider`, `EventType`, `Method`, `URL`, `StatusCode`, `DurationMs`.
- `Headers` as redacted JSON object, `Body` as redacted capped text, `Error`, `CreatedAt`.

The activity repository supports append-only create and transaction-scoped list by transaction ID, with a fallback lookup by merchant/reference for provider activity captured before transaction persistence.

Add migration `0004_transaction_activity.sql` with indexes on `(transaction_id, created_at)` and `(merchant_id, reference_id, created_at)`. Bodies are capped at 64 KiB before persistence.

## Capture boundaries

### Merchant API

Instrument `POST /v1/payments` at the handler boundary. Capture the redacted decoded request body, headers, method and URL. After success, capture the response envelope with the created transaction ID. For validation or provider failures where no transaction exists, do not create an orphan activity row; continue returning the existing API error.

### Provider API

Extend the shared provider HTTP tracing hook so adapters emit redacted request and response activity. The core places merchant/environment/reference/transaction context on the provider context before create, inquiry, and cancel calls. Provider activities may initially have no transaction ID and are joined by `(merchant_id, environment, reference_id)`; later requests carry the transaction ID directly.

The existing logger trace remains; persistence is an additional recorder and must not make payment calls fail if activity persistence fails. Activity persistence errors are logged and ignored from the payment outcome.

### Provider callbacks

Extend `InboundEvent` storage or activity creation at the receiver boundary so every callback attempt records the full redacted incoming request, signature result, parse result, applied transaction, and response status/body. Keep existing inbound-event persistence and reprocessing behavior unchanged. The activity direction is `provider_webhook`.

### Merchant webhooks

Inject the activity repository into the dispatcher. Each delivery attempt records a `merchant_webhook` request and response/event, including retry number, URL, response status/body, duration, and failure reason. Existing delivery status/backoff updates remain authoritative.

## Admin API

Add `GET /admin/api/transactions/{id}/activities` behind the existing session guard. Return newest-first or chronological order explicitly; the console will request chronological order with a bounded limit (default 200, max 500). Response data is safe redacted activity only.

## Console layout

- Header: reference/status/environment/provider, compact actions (`Back`, `Sync`, `Cancel`) in one horizontal action row that wraps on mobile.
- Summary: a dense two-column definition list on desktop, one column on narrow widths.
- Primary content: `Activity` timeline, one row per request/response/event, with direction label, timestamp, status, duration, and concise outcome.
- Each activity row expands to show redacted headers, body, error, and metadata. Only one row needs to be expanded at a time by default; the page must remain scan-friendly.
- Existing raw payment/customer data moves below the activity timeline in a secondary collapsible/tabbed section.
- Empty activity state says that no captured activity exists and distinguishes this from a loading or API error.

## Security and retention

- Redaction occurs before persistence, not only in the UI.
- Reuse the provider HTTP redaction rules for headers and JSON sensitive keys, extending them for merchant API keys, webhook signatures, passwords, and private keys.
- Cap stored bodies and render long bodies in a scrollable code block without horizontal page overflow.
- Activity query must enforce the transaction ID path scope and never accept a merchant ID override.

## Verification

- Repository and migration tests for ordering, scope, caps, and redaction.
- Core/API tests for merchant request/response activity and pre-persistence provider correlation.
- Provider trace tests for request/response capture without changing provider outcomes.
- Receiver/dispatcher tests for provider and merchant webhook activities, including retry failures.
- Admin API tests for scoped activity retrieval.
- Admin setup/typecheck/lint/build and focused console regression tests for timeline states and responsive structure.
- Full Go build, vet, tests, and race tests.
