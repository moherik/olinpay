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
import { IconChevronDown } from "@/components/icons";
import {
  TabsContent,
  TabsList,
  TabsRoot,
  TabsTrigger,
} from "@/components/ui/tabs";
import {
  apiGet,
  apiMutate,
  ApiError,
  mapTransaction,
  merchantName,
  type RawActivity,
  type RawTransaction,
} from "@/lib/api";
import { formatDateTime, formatIDR } from "@/lib/format";
import { activityBody, activityLabel, activityOrder, activityOutcome, activityTone } from "@/lib/transaction-activity";

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
  const { notify, merchants, auth, can } = useApp();

  const [trx, setTrx] = useState<RawTransaction | null>(null);
  const [meta, setMeta] = useState<Record<string, unknown>>({});
  const [activities, setActivities] = useState<RawActivity[]>([]);
  const [activityError, setActivityError] = useState<string | null>(null);
  const [expandedActivity, setExpandedActivity] = useState<string | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);

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
          const { data: rows } = await apiGet<RawActivity[]>(
            `/transactions/${params.id}/activities`,
            { limit: 500 },
          );
          setActivities(
            [...rows].sort((a, b) => {
              const rank = activityOrder(b.direction, b.phase) - activityOrder(a.direction, a.phase);
              return rank || Date.parse(b.created_at) - Date.parse(a.created_at);
            }),
          );
          setActivityError(null);
        } catch (cause) {
          setActivities([]);
          setActivityError(cause instanceof ApiError ? cause.message : "Cannot load transaction activity.");
        }
        setState("ready");
      } catch (cause) {
        setError(
          cause instanceof ApiError ? cause.message : "Cannot load transaction.",
        );
        setState("error");
      }
    },
    [params.id],
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
          </>
        }
      />

      <div className="grid gap-4">
        <div className="grid gap-4 md:grid-cols-2">
          <Panel>
            <PanelHeader
              title="Payment"
              action={
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    void navigator.clipboard?.writeText(mapped.id);
                    notify(`Copied ${mapped.id} to clipboard.`);
                  }}
                >
                  Copy id
                </Button>
              }
            />
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
        <ActivityPanel
          activities={activities}
          error={activityError}
          expanded={expandedActivity}
          onExpand={setExpandedActivity}
        />
      </div>
    </>
  );
}

function ActivityPanel({
  activities,
  error,
  expanded,
  onExpand,
}: {
  activities: RawActivity[];
  error: string | null;
  expanded: string | null;
  onExpand: (id: string | null) => void;
}) {
  return (
    <Panel>
      <PanelHeader
        title="Activity"
        hint="Every captured boundary from merchant request to provider and webhook delivery."
      />
      <PanelBody className="p-0">
        {error ? (
          <ApiFailed title="Activity could not be loaded" error={error} onRetry={() => window.location.reload()} />
        ) : activities.length === 0 ? (
          <EmptyState
            title="No activity recorded yet"
            body="This transaction has no persisted request, response, callback, or webhook activity."
          />
        ) : (
          <ol className="divide-y divide-border-subtle">
            {activities.map((item) => {
              const isOpen = expanded === item.id;
              const tone = activityTone(item.status_code, item.error);
              const toneClass =
                tone === "danger"
                  ? "text-failed"
                  : tone === "warn"
                    ? "text-pending"
                    : tone === "ok"
                      ? "text-paid"
                      : "text-muted";
              return (
                <li key={item.id}>
                  <button
                    type="button"
                    aria-expanded={isOpen}
                    onClick={() => onExpand(isOpen ? null : item.id)}
                    className="grid w-full grid-cols-[88px_1fr_auto] items-start gap-3 px-4 py-3 text-left transition-colors hover:bg-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                  >
                    <span className="num text-[12px] text-muted">
                      {formatDateTime(item.created_at)}
                    </span>
                    <span className="min-w-0">
                      <span className="block text-[13px] font-medium text-ink">
                        {activityLabel(item.direction, item.phase, item.method, item.url)}
                      </span>
                      <span className="block truncate text-[12px] text-secondary">
                        {item.phase || "Activity recorded"}
                      </span>
                    </span>
                    <span className="flex items-center justify-end gap-2">
                      <span className={`whitespace-nowrap text-[12px] ${toneClass}`}>
                        {activityOutcome(item.status_code, item.duration_ms, item.error)}
                      </span>
                      <IconChevronDown size={14} className={isOpen ? "rotate-180" : undefined} />
                    </span>
                  </button>
                  {isOpen ? (
                    <div className="grid min-w-0 max-w-full gap-3 overflow-hidden border-t border-border-subtle bg-surface-muted p-4">
                      {item.url ? <p className="break-all font-mono text-[12px] text-secondary">{item.url}</p> : null}
                      {item.headers && Object.keys(item.headers).length > 0 ? (
                        <CodeBlock label="headers.json" code={JSON.stringify(item.headers, null, 2)} />
                      ) : null}
                      {item.body ? <CodeBlock label="body.json" code={activityBody(item.body)} /> : null}
                      {item.error ? <p className="text-[13px] text-failed">{item.error}</p> : null}
                    </div>
                  ) : null}
                </li>
              );
            })}
          </ol>
        )}
      </PanelBody>
    </Panel>
  );
}
