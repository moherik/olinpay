"use client";

import { useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { Button } from "@/components/ui/button";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { Tag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import { CodeBlock } from "@/components/ui/code";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import {
  TabsContent,
  TabsList,
  TabsRoot,
  TabsTrigger,
} from "@/components/ui/tabs";
import {
  deliveries as seedDeliveries,
  inboundEvents as seedInbound,
  type Delivery,
} from "@/lib/demo-data";
import { formatDateTime } from "@/lib/format";

const deliveryTone = {
  delivered: "ok",
  pending: "warn",
  failed: "danger",
} as const;

export default function WebhooksPage() {
  const { environment, notify, actor } = useApp();
  const [rows, setRows] = useState<Delivery[]>(seedDeliveries);
  const [retrying, setRetrying] = useState<string | null>(null);
  const [payloadTarget, setPayloadTarget] = useState<Delivery | null>(null);
  const [reprocessTarget, setReprocessTarget] = useState<string | null>(null);

  const scoped = rows.filter((row) => row.environment === environment);
  const inbound = seedInbound.filter((row) => row.environment === environment);
  const failedCount = scoped.filter((row) => row.status === "failed").length;

  function retry(id: string) {
    const row = rows.find((entry) => entry.id === id);
    if (!row) return;
    setRetrying(id);
    setTimeout(() => {
      setRows((current) =>
        current.map((entry) =>
          entry.id === id
            ? {
                ...entry,
                status: "pending",
                attempts: 0,
                lastError: undefined,
                age: "just now",
              }
            : entry,
        ),
      );
      setRetrying(null);
      notify(
        `Requeued ${row.event} to ${row.merchant}. The dispatcher will retry with backoff.`,
      );
    }, 700);
  }

  return (
    <>
      <PageHeader
        title="Webhooks"
        description="Outbound deliveries the platform sent to merchant endpoints, and inbound callbacks received from gateways. Scoped to the environment selected above."
      />

      <TabsRoot defaultValue="outbound">
        <div className="mb-4">
          <TabsList>
            <TabsTrigger value="outbound" count={scoped.length}>
              Outbound deliveries
            </TabsTrigger>
            <TabsTrigger value="inbound" count={inbound.length}>
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
            {scoped.length === 0 ? (
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
                  {scoped.map((row) => (
                    <TR key={row.id}>
                      <TD className="font-mono text-[12.5px]">{row.event}</TD>
                      <TD>{row.merchant}</TD>
                      <TD
                        className="max-w-[240px] truncate font-mono text-[12px] text-secondary"
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
                      <TD className="max-w-[200px] truncate text-secondary">
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
                          {row.status === "failed" ? (
                            <Button
                              variant="secondary"
                              size="sm"
                              disabled={retrying === row.id}
                              onClick={() => retry(row.id)}
                            >
                              {retrying === row.id ? "Retrying\u2026" : "Retry"}
                            </Button>
                          ) : (
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled
                              title="Only a failed delivery needs a retry."
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
            {inbound.length === 0 ? (
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
                  {inbound.map((row) => (
                    <TR key={row.id}>
                      <TD className="font-mono text-[12.5px]">{row.provider}</TD>
                      <TD className="font-mono text-[12.5px]">{row.event}</TD>
                      <TD className="font-mono text-[12.5px]">{row.reference}</TD>
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
                      <TD className="max-w-[240px] text-secondary">
                        {row.note ?? "\u2014"}
                      </TD>
                      <TD className="text-right">
                        {row.signatureValid && !row.parsed ? (
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => setReprocessTarget(row.id)}
                          >
                            Reprocess
                          </Button>
                        ) : (
                          <span className="text-[12px] text-muted">
                            {row.signatureValid ? "Applied" : "Rejected"}
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
              ? `Delivered to ${payloadTarget.url} for ${payloadTarget.merchant}.`
              : undefined
          }
        >
          {payloadTarget ? (
            <CodeBlock
              label="delivery.json"
              code={JSON.stringify(
                {
                  id: payloadTarget.id,
                  event: payloadTarget.event,
                  environment: payloadTarget.environment,
                  merchant: payloadTarget.merchant,
                  status: payloadTarget.status,
                  attempts: payloadTarget.attempts,
                  last_error: payloadTarget.lastError ?? null,
                  updated_at: payloadTarget.updatedAt,
                },
                null,
                2,
              )}
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
              <Button variant="secondary" onClick={() => setReprocessTarget(null)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                onClick={() => {
                  notify(
                    `Reprocess queued by ${actor}. Matching by reference keeps this safe to run twice.`,
                  );
                  setReprocessTarget(null);
                }}
              >
                Reprocess callback
              </Button>
            </>
          }
        >
          <p className="text-[13px] text-secondary">
            Reprocessing is matched on the payment reference, so applying the
            same callback twice does not change the payment state again.
          </p>
        </DialogContent>
      </DialogRoot>
    </>
  );
}
