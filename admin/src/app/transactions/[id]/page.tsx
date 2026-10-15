"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { DefList, DefRow, Mono } from "@/components/kv";
import { Button } from "@/components/ui/button";
import { Panel, PanelBody, PanelHeader } from "@/components/ui/panel";
import { StatusTag } from "@/components/ui/status-tag";
import { ErrorState, LoadingState } from "@/components/ui/empty-state";
import { CodeBlock } from "@/components/ui/code";
import {
  TabsContent,
  TabsList,
  TabsRoot,
  TabsTrigger,
} from "@/components/ui/tabs";
import type { Transaction } from "@/lib/demo-data";
import { formatDateTime, formatIDR } from "@/lib/format";

type TimelineEvent = { at: string; label: string; detail: string };

function buildEvents(trx: Transaction): TimelineEvent[] {
  const base = new Date(trx.createdAt).getTime();
  const at = (offsetSeconds: number) =>
    new Date(base + offsetSeconds * 1000).toISOString();

  const events: TimelineEvent[] = [
    {
      at: trx.createdAt,
      label: "Payment created",
      detail: `${trx.provider} accepted the create request over ${trx.channel}.`,
    },
    {
      at: at(2),
      label: "Gateway acknowledged",
      detail: `Provider reference ${trx.providerReference}.`,
    },
  ];

  if (trx.status === "paid") {
    events.push({
      at: at(94),
      label: "Callback received",
      detail: "payment.paid, signature verified, amount matched.",
    });
  } else if (trx.status === "pending") {
    events.push({
      at: at(30),
      label: "Awaiting payment",
      detail: "No callback yet. The payment window is still open.",
    });
  } else if (trx.status === "failed") {
    events.push({
      at: at(48),
      label: "Payment failed",
      detail: trx.failureReason ?? "Gateway reported a failure.",
    });
  } else if (trx.status === "expired") {
    events.push({
      at: at(3600),
      label: "Payment expired",
      detail: "The payment window closed before the customer paid.",
    });
  } else if (trx.status === "cancelled") {
    events.push({
      at: at(62),
      label: "Payment cancelled",
      detail: "Cancellation confirmed by the gateway.",
    });
  } else if (trx.status === "refunded") {
    events.push({
      at: at(120),
      label: "Callback received",
      detail: "payment.paid, signature verified, amount matched.",
    });
    events.push({
      at: at(7200),
      label: "Refund settled",
      detail: "Refund confirmed by the gateway.",
    });
  } else {
    events.push({
      at: at(140),
      label: "Outcome unresolved",
      detail:
        trx.failureReason ??
        "The gateway did not return a final status. Reconciliation is required.",
    });
  }

  return events;
}

function buildPayload(trx: Transaction) {
  return JSON.stringify(
    {
      event: `payment.${trx.status}`,
      environment: trx.environment,
      merchant: trx.merchant,
      provider: trx.provider,
      method: trx.method,
      channel: trx.channel,
      reference: trx.reference,
      provider_reference: trx.providerReference,
      amount: trx.amount,
      currency: trx.currency,
      status: trx.status,
      created_at: trx.createdAt,
    },
    null,
    2,
  );
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
        <LoadingState label="Loading transaction" />
      </Panel>
    </>
  );
}

function TransactionDetailInner() {
  const params = useParams<{ id: string }>();
  const { notify, transactions, updateTransaction } = useApp();
  const trx = transactions.find((row) => row.id === params.id);

  const [eventsState, setEventsState] = useState<"loading" | "ready" | "error">(
    "loading",
  );
  const [syncing, setSyncing] = useState(false);

  const needsReconciliation = trx?.status === "unknown";

  useEffect(() => {
    const timer = setTimeout(() => {
      setEventsState(needsReconciliation ? "error" : "ready");
    }, 500);
    return () => clearTimeout(timer);
  }, [needsReconciliation, params.id]);

  if (!trx) {
    return (
      <>
        <PageHeader title="Transaction not found" />
        <Panel>
          <ErrorState
            title="No transaction with that id"
            body="The id in the address does not match any transaction in this demo set. It may have been mistyped, or it belongs to another environment."
            action={
              <Button asChild variant="secondary">
                <Link href="/transactions">Back to transactions</Link>
              </Button>
            }
          />
        </Panel>
      </>
    );
  }

  const events = buildEvents(trx);

  return (
    <>
      <PageHeader
        title={trx.reference}
        meta={<StatusTag status={trx.status} />}
        description={`${trx.merchant} in ${trx.environment} via ${trx.provider}. Created ${formatDateTime(
          trx.createdAt,
        )}.`}
        actions={
          <>
            <Button asChild variant="ghost">
              <Link href="/transactions">Back to list</Link>
            </Button>
            <Button
              variant="secondary"
              disabled={syncing}
              onClick={() => {
                setSyncing(true);
                setTimeout(() => {
                  setSyncing(false);
                  notify(
                    `Sync finished for ${trx.reference}. No change: the status already matches the gateway.`,
                  );
                }, 900);
              }}
            >
              {syncing ? "Syncing\u2026" : "Sync from provider"}
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
                  <Mono>{trx.reference}</Mono>
                </DefRow>
                <DefRow label="Amount">
                  <span className="num">{formatIDR(trx.amount)}</span>
                </DefRow>
                <DefRow label="Merchant">{trx.merchant}</DefRow>
                <DefRow label="Environment">{trx.environment}</DefRow>
                <DefRow label="Provider">
                  <Mono>{trx.provider}</Mono>
                </DefRow>
                <DefRow label="Method">
                  {trx.method} / {trx.channel}
                </DefRow>
                <DefRow label="Provider reference">
                  <Mono>{trx.providerReference}</Mono>
                </DefRow>
                <DefRow label="Created">
                  <span className="num">{formatDateTime(trx.createdAt)}</span>
                </DefRow>
                {trx.failureReason ? (
                  <DefRow label="Note">{trx.failureReason}</DefRow>
                ) : null}
              </DefList>
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader
              title="Provider events"
              hint="What the platform sent to the gateway and what came back."
            />
            <PanelBody>
              {eventsState === "loading" ? (
                <LoadingState label="Loading provider events" />
              ) : eventsState === "error" ? (
                <ErrorState
                  title="Provider events could not be loaded"
                  body="The platform has no stored event history for this transaction yet. Outbound provider events are not persisted in this build, so an unknown outcome has nothing to show."
                  action={
                    <Button
                      variant="secondary"
                      onClick={() => {
                        setEventsState("loading");
                        setTimeout(() => setEventsState("ready"), 700);
                      }}
                    >
                      Retry
                    </Button>
                  }
                />
              ) : (
                <ol className="flex flex-col">
                  {events.map((event, index) => (
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
              title="Raw payload"
              hint="The last request sent to the gateway and the callback body."
            />
            <PanelBody>
              <TabsRoot defaultValue="callback">
                <div className="mb-3">
                  <TabsList>
                    <TabsTrigger value="callback">Callback body</TabsTrigger>
                    <TabsTrigger value="request">Provider request</TabsTrigger>
                  </TabsList>
                </div>
                <TabsContent value="callback">
                  <CodeBlock
                    label="callback.json"
                    code={buildPayload(trx)}
                  />
                </TabsContent>
                <TabsContent value="request">
                  <CodeBlock
                    label="create-payment.request.json"
                    code={JSON.stringify(
                      {
                        reference: trx.reference,
                        amount: trx.amount,
                        currency: trx.currency,
                        method: trx.method,
                        channel_code: trx.channel,
                        callback_url: `https://openpay.example/webhooks/${trx.provider}`,
                      },
                      null,
                      2,
                    )}
                  />
                </TabsContent>
              </TabsRoot>
            </PanelBody>
          </Panel>
        </div>

        <div className="grid gap-4">
          <Panel>
            <PanelHeader title="Status timeline" />
            <PanelBody>
              <ol className="flex flex-col gap-2.5">
                <li className="text-[13px] text-ink">
                  pending
                  <span className="num ml-2 text-[12px] text-muted">
                    {formatDateTime(trx.createdAt)}
                  </span>
                </li>
                {trx.status !== "pending" ? (
                  <li className="text-[13px] text-ink">
                    {trx.status}
                    <span className="num ml-2 text-[12px] text-muted">
                      {formatDateTime(
                        new Date(
                          new Date(trx.createdAt).getTime() + 94_000,
                        ).toISOString(),
                      )}
                    </span>
                  </li>
                ) : (
                  <li className="text-[13px] text-muted">
                    No further transition yet.
                  </li>
                )}
              </ol>
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader title="Actions" />
            <PanelBody className="flex flex-col items-start gap-2">
              <Button
                variant="secondary"
                disabled={trx.status !== "pending"}
                onClick={() => {
                  updateTransaction(trx.id, { status: "cancelled" });
                  notify(`Cancellation recorded for ${trx.reference}.`);
                }}
              >
                Cancel payment
              </Button>
              <p className="text-[12px] text-muted">
                {trx.status === "pending"
                  ? "Only a pending payment can be cancelled."
                  : "This payment is in a final state and cannot be cancelled."}
              </p>
              <Button
                variant="ghost"
                onClick={() => {
                  void navigator.clipboard?.writeText(trx.id);
                  notify(`Copied ${trx.id} to clipboard.`);
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
