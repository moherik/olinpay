"use client";

import { useCallback, useEffect, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { ApiFailed, ApiLoading } from "@/components/ui/api-state";
import { Button } from "@/components/ui/button";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { Tag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import { CodeBlock } from "@/components/ui/code";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  TabsContent,
  TabsList,
  TabsRoot,
  TabsTrigger,
} from "@/components/ui/tabs";
import type { Delivery } from "@/lib/demo-data";
import {
  apiGet,
  apiMutate,
  ApiError,
  mapDelivery,
  mapInbound,
  merchantName,
  type RawDelivery,
  type RawInbound,
  type RawTransaction,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";

const deliveryTone = {
  delivered: "ok",
  pending: "warn",
  failed: "danger",
} as const;

export default function WebhooksPage() {
  const { environment, notify, merchants } = useApp();
  const [deliveries, setDeliveries] = useState<Delivery[]>([]);
  const [inbound, setInbound] = useState<RawInbound[]>([]);
  const [deliveryCount, setDeliveryCount] = useState(0);

  const [loadingOut, setLoadingOut] = useState(true);
  const [loadingIn, setLoadingIn] = useState(true);
  const [errorOut, setErrorOut] = useState<string | null>(null);
  const [errorIn, setErrorIn] = useState<string | null>(null);

  const [retrying, setRetrying] = useState<string | null>(null);
  const [payloadTarget, setPayloadTarget] = useState<Delivery | null>(null);
  const [reprocessing, setReprocessing] = useState(false);
  const [reprocessTarget, setReprocessTarget] = useState<string | null>(null);

  const loadOutbound = useCallback(async () => {
    setLoadingOut(true);
    setErrorOut(null);
    try {
      const { data, meta } = await apiGet<RawDelivery[]>("/deliveries", {
        env: environment,
        limit: 100,
      });
      setDeliveries(data.map(mapDelivery));
      setDeliveryCount(Number(meta.count ?? data.length));
    } catch (cause) {
      setErrorOut(
        cause instanceof ApiError
          ? cause.message
          : "Cannot load webhook deliveries.",
      );
    } finally {
      setLoadingOut(false);
    }
  }, [environment]);

  const loadInbound = useCallback(async () => {
    setLoadingIn(true);
    setErrorIn(null);
    try {
      const { data } = await apiGet<RawInbound[]>("/inbound", {
        env: environment,
        limit: 100,
      });
      setInbound(data);
    } catch (cause) {
      setErrorIn(
        cause instanceof ApiError
          ? cause.message
          : "Cannot load inbound callbacks.",
      );
    } finally {
      setLoadingIn(false);
    }
  }, [environment]);

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const outbound = setTimeout(() => void loadOutbound(), 0);
    const inbound = setTimeout(() => void loadInbound(), 0);
    return () => {
      clearTimeout(outbound);
      clearTimeout(inbound);
    };
  }, [loadOutbound, loadInbound]);

  const failedCount = deliveries.filter((row) => row.status === "failed").length;
  const inboundRows = inbound.map((row) => mapInbound(row, environment));

  async function retry(row: Delivery) {
    setRetrying(row.id);
    try {
      const { data } = await apiMutate<RawDelivery>(
        "POST",
        `/deliveries/${row.id}/retry`,
      );
      setDeliveries((current) =>
        current.map((entry) =>
          entry.id === row.id ? mapDelivery(data) : entry,
        ),
      );
      notify(
        `Requeued ${row.event} to ${merchantName(row.merchant, merchants)}. The dispatcher will retry with backoff.`,
      );
    } catch (cause) {
      notify(
        cause instanceof ApiError
          ? `Retry failed: ${cause.message}`
          : "Retry failed.",
      );
    } finally {
      setRetrying(null);
    }
  }

  async function reprocess(id: string) {
    setReprocessing(true);
    try {
      const { data } = await apiMutate<{
        event: RawInbound;
        transaction?: RawTransaction;
      }>("POST", `/inbound/${id}/reprocess`);
      if (data.transaction) {
        notify(
          `Reprocess applied: ${data.transaction.reference_id} is now ${data.transaction.status}. Matching on reference keeps this safe to run twice.`,
        );
      } else {
        notify("Reprocess complete. No transaction was correlated.");
      }
      void loadInbound();
      void loadOutbound();
    } catch (cause) {
      notify(
        cause instanceof ApiError
          ? `Reprocess failed: ${cause.message}`
          : "Reprocess failed.",
      );
    } finally {
      setReprocessing(false);
      setReprocessTarget(null);
    }
  }

  const reprocessCandidate = inboundRows.find(
    (row) => row.id === reprocessTarget,
  );

  return (
    <>
      <PageHeader
        title="Webhooks"
        description="Outbound deliveries the platform sent to merchant endpoints, and inbound callbacks received from gateways. Scoped to the environment selected above."
      />

      <TabsRoot defaultValue="outbound">
        <div className="mb-4">
          <TabsList>
            <TabsTrigger value="outbound" count={deliveryCount}>
              Outbound deliveries
            </TabsTrigger>
            <TabsTrigger value="inbound" count={inboundRows.length}>
              Inbound callbacks
            </TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="outbound">
          <Panel>
            <PanelHeader
              title="Outbound deliveries"
              hint={
                failedCount > 0
                  ? `${failedCount} failed after every automatic attempt. Retry requeues the delivery.`
                  : "No delivery has exhausted its attempts."
              }
            />
            {errorOut && deliveries.length === 0 ? (
              <ApiFailed
                title="Deliveries unavailable"
                error={errorOut}
                onRetry={() => void loadOutbound()}
              />
            ) : loadingOut && deliveries.length === 0 ? (
              <ApiLoading label="Loading deliveries…" />
            ) : deliveries.length === 0 ? (
              <EmptyState
                title="No deliveries in this environment"
                body="The platform has not queued an outbound delivery here yet. Deliveries appear once a payment changes state."
              />
            ) : (
              <Table>
                <THead>
                  <TR className="hover:bg-surface-muted">
                    <TH>Event</TH>
                    <TH>Merchant</TH>
                    <TH>Endpoint</TH>
                    <TH>Status</TH>
                    <TH className="text-right">Attempts</TH>
                    <TH>Last error</TH>
                    <TH>Updated</TH>
                    <TH className="text-right">Action</TH>
                  </TR>
                </THead>
                <TBody>
                  {deliveries.map((row) => (
                    <TR key={row.id}>
                      <TD className="font-mono text-[12.5px]">{row.event}</TD>
                      <TD>{merchantName(row.merchant, merchants)}</TD>
                      <TD
                        className="max-w-[280px] truncate font-mono text-[12px] text-secondary"
                        title={row.url}
                      >
                        {row.url}
                      </TD>
                      <TD>
                        <Tag tone={deliveryTone[row.status]}>{row.status}</Tag>
                      </TD>
                      <TD className="num text-right">
                        {row.attempts} / {row.maxAttempts}
                      </TD>
                      <TD className="max-w-[240px] truncate text-secondary">
                        {row.lastError ?? "\u2014"}
                      </TD>
                      <TD className="num whitespace-nowrap text-secondary">
                        {formatDateTime(row.updatedAt)}
                      </TD>
                      <TD className="text-right">
                        <div className="flex items-center justify-end gap-1">
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => setPayloadTarget(row)}
                          >
                            Payload
                          </Button>
                          {row.status !== "pending" ? (
                            <ConfirmDialog
                              trigger={
                                <Button
                                  variant="secondary"
                                  size="sm"
                                  disabled={retrying === row.id}
                                >
                                  {retrying === row.id ? "Retrying\u2026" : "Retry"}
                                </Button>
                              }
                              title="Retry webhook delivery?"
                              description={`Retry delivery of ${row.event} to ${merchantName(row.merchant, merchants)}? This will requeue with backoff.`}
                              confirmLabel="Retry"
                              confirmVariant="primary"
                              onConfirm={() => void retry(row)}
                            />
                          ) : (
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled
                              title="The dispatcher is still working on this delivery."
                            >
                              Retry
                            </Button>
                          )}
                        </div>
                      </TD>
                    </TR>
                  ))}
                </TBody>
              </Table>
            )}
          </Panel>
        </TabsContent>

        <TabsContent value="inbound">
          <Panel>
            <PanelHeader
              title="Inbound callbacks"
              hint="Callbacks the platform received from gateways. A rejected signature means the callback was not applied."
            />
            {errorIn && inboundRows.length === 0 ? (
              <ApiFailed
                title="Callbacks unavailable"
                error={errorIn}
                onRetry={() => void loadInbound()}
              />
            ) : loadingIn && inboundRows.length === 0 ? (
              <ApiLoading label="Loading callbacks…" />
            ) : inboundRows.length === 0 ? (
              <EmptyState
                title="No callbacks in this environment"
                body="No gateway has called back here yet. Callbacks appear when a gateway reports a payment state change."
              />
            ) : (
              <Table>
                <THead>
                  <TR className="hover:bg-surface-muted">
                    <TH>Provider</TH>
                    <TH>Event</TH>
                    <TH>Reference</TH>
                    <TH>Signature</TH>
                    <TH>Parsed</TH>
                    <TH>Received</TH>
                    <TH>Note</TH>
                    <TH className="text-right">Action</TH>
                  </TR>
                </THead>
                <TBody>
                  {inboundRows.map((row) => (
                    <TR key={row.id}>
                      <TD className="font-mono text-[12.5px]">{row.provider}</TD>
                      <TD className="font-mono text-[12.5px]">{row.event || "\u2014"}</TD>
                      <TD className="font-mono text-[12.5px]">{row.reference || "\u2014"}</TD>
                      <TD>
                        <Tag tone={row.signatureValid ? "ok" : "danger"}>
                          {row.signatureValid ? "valid" : "invalid"}
                        </Tag>
                      </TD>
                      <TD>
                        <Tag tone={row.parsed ? "ok" : "warn"}>
                          {row.parsed ? "yes" : "no"}
                        </Tag>
                      </TD>
                      <TD className="num whitespace-nowrap text-secondary">
                        {formatDateTime(row.receivedAt)}
                      </TD>
                      <TD className="max-w-[280px] text-secondary">
                        {row.signatureValid && !row.parsed ? null : row.applied ? (
                          "Applied"
                        ) : (
                          (row.note ?? "\u2014")
                        )}
                      </TD>
                      <TD className="text-right">
                        {row.signatureValid && row.parsed && !row.applied ? (
                          <ConfirmDialog
                            trigger={
                              <Button variant="secondary" size="sm">
                                Reprocess
                              </Button>
                            }
                            title="Reprocess this callback?"
                            description="The signature is valid, so the payload is trusted. The platform will look up the payment and apply the state it reports."
                            confirmLabel="Reprocess"
                            confirmVariant="primary"
                            disabled={reprocessing}
                            onConfirm={() => setReprocessTarget(row.id)}
                          />
                        ) : (
                          <span className="text-[12px] text-muted">
                            {row.signatureValid && row.parsed ? "Applied" : "Rejected"}
                          </span>
                        )}
                      </TD>
                    </TR>
                  ))}
                </TBody>
              </Table>
            )}
          </Panel>
        </TabsContent>
      </TabsRoot>

      <DialogRoot
        open={payloadTarget !== null}
        onOpenChange={(open) => {
          if (!open) setPayloadTarget(null);
        }}
      >
        <DialogContent
          title={`${payloadTarget?.event ?? "Delivery"} payload`}
          description={
            payloadTarget
              ? `Delivered to ${payloadTarget.url} for ${merchantName(payloadTarget.merchant, merchants)}.`
              : undefined
          }
        >
          {payloadTarget ? (
            <CodeBlock
              label="delivery.json"
              code={
                payloadTarget.payload ||
                JSON.stringify(
                  {
                    id: payloadTarget.id,
                    event: payloadTarget.event,
                    environment: payloadTarget.environment,
                    status: payloadTarget.status,
                    attempts: payloadTarget.attempts,
                    last_error: payloadTarget.lastError ?? null,
                    updated_at: payloadTarget.updatedAt,
                  },
                  null,
                  2,
                )
              }
            />
          ) : null}
        </DialogContent>
      </DialogRoot>

      <DialogRoot
        open={reprocessTarget !== null}
        onOpenChange={(open) => {
          if (!open) setReprocessTarget(null);
        }}
      >
        <DialogContent
          title="Reprocess this callback?"
          description="The signature is valid, so the payload is trusted. The platform will look up the payment and apply the state it reports."
          footer={
            <>
              <Button
                variant="secondary"
                onClick={() => setReprocessTarget(null)}
              >
                Cancel
              </Button>
              <Button
                variant="primary"
                disabled={reprocessing}
                onClick={() => {
                  if (reprocessTarget) {
                    void reprocess(reprocessTarget);
                  }
                }}
              >
                {reprocessing ? "Reprocessing…" : "Reprocess"}
              </Button>
            </>
          }
        >
          <p className="text-[13px] text-muted">
            {reprocessCandidate?.reference
              ? `Matched on reference ${reprocessCandidate.reference}; running twice is safe — matching keeps this idempotent.`
              : "Matching runs on the payment reference."}
          </p>
        </DialogContent>
      </DialogRoot>
    </>
  );
}
