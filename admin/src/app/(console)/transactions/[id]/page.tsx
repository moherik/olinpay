"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { DefList, DefRow, Mono } from "@/components/kv";
import { ApiFailed, ApiLoading } from "@/components/ui/api-state";
import { Button } from "@/components/ui/button";
import { Panel, PanelBody, PanelHeader } from "@/components/ui/panel";
import { StatusTag } from "@/components/ui/status-tag";
import { EmptyState } from "@/components/ui/empty-state";
import { CodeBlock } from "@/components/ui/code";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  TabsContent,
  TabsList,
  TabsRoot,
  TabsTrigger,
} from "@/components/ui/tabs";
import type { Transaction } from "@/lib/demo-data";
import {
  apiGet,
  apiMutate,
  ApiError,
  mapInbound,
  mapTransaction,
  merchantName,
  type RawInbound,
  type RawTransaction,
} from "@/lib/api";
import { formatDateTime, formatIDR } from "@/lib/format";

type TimelineEvent = { at: string; label: string; detail: string };

function buildEvents(
  trx: Transaction,
  inbound: TimelineEvent[],
): TimelineEvent[] {
  const events: TimelineEvent[] = [
    {
      at: trx.createdAt,
      label: "Payment created",
      detail: `${trx.provider} accepted the create request over ${trx.channel || trx.method}.`,
    },
  ];
  events.push(...inbound);
  return events;
}

export default function TransactionDetailPage() {
  return (
    <Suspense fallback={<DetailFallback />}>
      <TransactionDetailInner />
    </Suspense>
  );
}

function DetailFallback() {
  return (
    <>
      <PageHeader title="Transaction" description="Loading this payment." />
      <Panel>
        <ApiLoading label="Loading transaction" />
      </Panel>
    </>
  );
}

function TransactionDetailInner() {
  const params = useParams<{ id: string }>();
  const { notify, merchants, auth, can, environment } = useApp();

  const [trx, setTrx] = useState<RawTransaction | null>(null);
  const [meta, setMeta] = useState<Record<string, unknown>>({});
  const [inbound, setInbound] = useState<TimelineEvent[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [cancelledMessage, setCancelledMessage] = useState("");

  const load = useCallback(
    async (sync: boolean) => {
      setState("loading");
      setError(null);
      try {
        const { data, meta: txMeta } = await apiGet<RawTransaction>(
          `/transactions/${params.id}`,
          { sync: sync ? 1 : undefined },
        );
        setTrx(data);
        setMeta(txMeta);
        try {
          const { data: events } = await apiGet<RawInbound[]>("/inbound", {
            transaction_id: params.id,
            env: data.environment,
          });
          setInbound(
            events.map((event) => mapInbound(event, environment)).map((entry) => ({
              at: entry.receivedAt,
              label: `Callback: ${entry.event || "unlabelled"}`,
              detail: `${entry.provider} reported the outcome${
                entry.applied ? "; applied to the payment" : "; not applied"
              }${entry.signatureValid ? ", signature verified" : ", signature NOT verified"}.`,
            })),
          );
        } catch {
          setInbound([]);
        }
        setState("ready");
      } catch (cause) {
        setError(
          cause instanceof ApiError ? cause.message : "Cannot load transaction.",
        );
        setState("error");
      }
    },
    [params.id, environment],
  );

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(false), 0);
    return () => clearTimeout(timer);
  }, [load]);

  if (state === "loading" && !trx) {
    return (
      <>
        <PageHeader title="Transaction" description="Loading this payment." />
        <Panel>
          <ApiLoading label="Loading transaction" />
        </Panel>
      </>
    );
  }

  if (state === "error" || !trx) {
    return (
      <>
        <PageHeader title="Transaction unavailable" />
        <Panel>
          {error === "Transaction not found" ? (
            <EmptyState
              title="No transaction with that id"
              body="The id in the address does not match any stored transaction. It may have been mistyped, or it belongs to another environment."
              action={
                <Button asChild variant="secondary">
                  <Link href="/transactions">Back to transactions</Link>
                </Button>
              }
            />
          ) : (
            <ApiFailed
              title="Transaction could not be loaded"
              error={error}
              onRetry={() => void load(false)}
            />
          )}
        </Panel>
      </>
    );
  }

  const mapped = mapTransaction(trx);
  const timeline = buildEvents(mapped, inbound);
  const syncAvailable = meta.can_sync === true;
  const cancelAvailable =
    meta.can_cancel === true &&
    can("cancel") &&
    auth.ready &&
    auth.authenticated &&
    auth.user.role !== "tech";

  async function cancelPayment() {
    if (!trx) return;
    try {
      const { data } = await apiMutate<RawTransaction>(
        "POST",
        `/transactions/${trx.id}/cancel`,
        { reason: "Cancelled from the transaction detail page" },
      );
      setTrx(data);
      setCancelledMessage("Cancellation confirmed.");
      notify(`Cancellation recorded for ${mapped.reference}.`);
      void load(false);
    } catch (cause) {
      notify(
        cause instanceof ApiError
          ? `Cancel failed: ${cause.message}`
          : "Cancel failed — the gateway did not accept it.",
      );
    }
  }

  return (
    <>
      <PageHeader
        title={mapped.reference}
        meta={<StatusTag status={mapped.status} />}
        description={`${merchantName(mapped.merchant, merchants)} in ${mapped.environment} via ${mapped.provider}. Created ${formatDateTime(
          mapped.createdAt,
        )}.`}
        actions={
          <>
            <Button asChild variant="ghost">
              <Link href="/transactions">Back to list</Link>
            </Button>
            <Button
              variant="secondary"
              disabled={!syncAvailable || syncing}
              onClick={async () => {
                setSyncing(true);
                try {
                  await load(true);
                  setCancelledMessage("");
                  notify(
                    `Sync finished for ${mapped.reference}: the stored status now matches the gateway.`,
                  );
                } catch {
                  notify("Sync failed — the gateway did not respond.");
                } finally {
                  setSyncing(false);
                }
              }}
            >
              {syncing ? "Syncing…" : "Sync from provider"}
            </Button>
          </>
        }
      />

      <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
        <div className="grid gap-4">
          <Panel>
            <PanelHeader title="Payment" />
            <PanelBody>
              <DefList>
                <DefRow label="Reference">
                  <Mono>{mapped.reference}</Mono>
                </DefRow>
                <DefRow label="Amount">
                  <span className="num">{formatIDR(mapped.amount)}</span>
                </DefRow>
                <DefRow label="Merchant">
                  {merchantName(mapped.merchant, merchants)}
                </DefRow>
                <DefRow label="Environment">{mapped.environment}</DefRow>
                <DefRow label="Provider">
                  <Mono>{mapped.provider}</Mono>
                </DefRow>
                <DefRow label="Method">
                  {mapped.method}
                  {mapped.channel ? ` / ${mapped.channel}` : ""}
                </DefRow>
                <DefRow label="Provider reference">
                  <Mono>{mapped.providerReference || "—"}</Mono>
                </DefRow>
                <DefRow label="Created">
                  <span className="num">{formatDateTime(mapped.createdAt)}</span>
                </DefRow>
                {trx.payment_code ? (
                  <DefRow label="Payment code">
                    <Mono>{trx.payment_code}</Mono>
                  </DefRow>
                ) : null}
                {trx.expires_at ? (
                  <DefRow label="Expires">
                    <span className="num">{formatDateTime(trx.expires_at)}</span>
                  </DefRow>
                ) : null}
                {trx.paid_at ? (
                  <DefRow label="Paid">
                    <span className="num">{formatDateTime(trx.paid_at)}</span>
                  </DefRow>
                ) : null}
                {mapped.failureReason ? (
                  <DefRow label="Note">{mapped.failureReason}</DefRow>
                ) : null}
              </DefList>
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader
              title="Provider callbacks"
              hint="What the gateway reported back, and whether the signature verified."
            />
            <PanelBody>
              {timeline.length <= 1 ? (
                <EmptyState
                  title="No callbacks recorded"
                  body="The gateway has not called back for this payment yet, or the callback history is not stored for this environment."
                />
              ) : (
                <ol className="flex flex-col">
                  {timeline.map((event, index) => (
                    <li
                      key={`${event.label}-${index}`}
                      className="grid grid-cols-[104px_1fr] gap-3 border-b border-border-subtle py-2.5 last:border-b-0"
                    >
                      <span className="num text-[12px] text-muted">
                        {formatDateTime(event.at)}
                      </span>
                      <span>
                        <span className="block text-[13px] text-ink">
                          {event.label}
                        </span>
                        <span className="block text-[12px] text-secondary">
                          {event.detail}
                        </span>
                      </span>
                    </li>
                  ))}
                </ol>
              )}
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader
              title="Raw record"
              hint="The stored payment record, exactly as the engine holds it."
            />
            <PanelBody>
              <TabsRoot defaultValue="payment">
                <div className="mb-3">
                  <TabsList>
                    <TabsTrigger value="payment">Payment record</TabsTrigger>
                    <TabsTrigger value="customer">Customer</TabsTrigger>
                  </TabsList>
                </div>
                <TabsContent value="payment">
                  <CodeBlock
                    label="payment.json"
                    code={JSON.stringify(trx, null, 2)}
                  />
                </TabsContent>
                <TabsContent value="customer">
                  {trx.customer ? (
                    <CodeBlock
                      label="customer.json"
                      code={JSON.stringify(trx.customer, null, 2)}
                    />
                  ) : (
                    <EmptyState
                      title="No customer block"
                      body="The creating integration did not pass a customer object."
                    />
                  )}
                </TabsContent>
              </TabsRoot>
            </PanelBody>
          </Panel>
        </div>

        <div className="grid gap-4">
          <Panel>
            <PanelHeader title="Actions" />
            <PanelBody className="flex flex-col items-start gap-2">
              <ConfirmDialog
                trigger={
                  <Button variant="secondary" disabled={!cancelAvailable}>
                    Cancel payment
                  </Button>
                }
                title={`Cancel ${mapped.reference}?`}
                description="Cancelling tells the gateway to void the payment. A pending payment can still be paid until the gateway confirms."
                confirmLabel={cancelAvailable ? "Cancel payment" : undefined}
                onConfirm={() => void cancelPayment()}
              />
              <p className="text-[12px] text-muted">
                {!cancelAvailable
                  ? meta.can_cancel !== true
                    ? "This payment cannot be cancelled by its provider, or it already reached a final state."
                    : "Cancelling requires the operator or owner role."
                  : "Only a pending payment can be cancelled."}
              </p>
              {cancelledMessage ? (
                <p className="text-[12px] text-secondary">{cancelledMessage}</p>
              ) : null}
              <Button
                variant="ghost"
                onClick={() => {
                  void navigator.clipboard?.writeText(mapped.id);
                  notify(`Copied ${mapped.id} to clipboard.`);
                }}
              >
                Copy transaction id
              </Button>
            </PanelBody>
          </Panel>
        </div>
      </div>
    </>
  );
}
