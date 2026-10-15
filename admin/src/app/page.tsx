"use client";

import Link from "next/link";
import { HourlyBars, MiniBar } from "@/components/charts";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { DefList, DefRow } from "@/components/kv";
import { Panel, PanelBody, PanelHeader } from "@/components/ui/panel";
import { Tag } from "@/components/ui/status-tag";
import { Button } from "@/components/ui/button";
import {
  deliveries,
  failedPerHour,
  paymentsPerHour,
  providerReliability,
  statusBreakdown,
  type Status,
} from "@/lib/demo-data";
import { formatCount } from "@/lib/format";

const statusOrder: Status[] = [
  "paid",
  "pending",
  "expired",
  "failed",
  "cancelled",
  "refunded",
  "unknown",
];

export default function OverviewPage() {
  const { environment } = useApp();

  const counts = Object.fromEntries(
    statusBreakdown.map((row) => [row.status, row.count]),
  ) as Record<Status, number>;
  const total = statusBreakdown.reduce((sum, row) => sum + row.count, 0);

  const scopedDeliveries = deliveries.filter(
    (delivery) => delivery.environment === environment,
  );
  const deliveryStates = [
    { state: "delivered", label: "Delivered" },
    { state: "pending", label: "Pending retry" },
    { state: "failed", label: "Failed" },
  ].map((entry) => ({
    ...entry,
    count: scopedDeliveries.filter((d) => d.status === entry.state).length,
  }));
  const deliveryMax = Math.max(...deliveryStates.map((s) => s.count), 1);

  const failedDeliveries = scopedDeliveries.filter(
    (d) => d.status === "failed",
  ).length;

  return (
    <>
      <PageHeader
        title="Overview"
        description="Platform activity for the environment selected above. Every figure on this screen is sample data for the design review, not platform data."
      />

      <div className="grid gap-4">
        <Panel>
          <PanelHeader
            title="Needs attention"
            hint="Items an operator would act on next."
          />
          <PanelBody className="flex flex-col gap-3">
            <AttentionRow
              severity={failedDeliveries > 0 ? "danger" : "ok"}
              label="Failed webhook deliveries"
              value={`${failedDeliveries} in ${environment}`}
              href="/webhooks"
              action="Open outbound deliveries"
            />
            <AttentionRow
              severity="warn"
              label="Transactions awaiting reconciliation"
              value="1 unknown outcome"
              href="/transactions/trx_11"
              action="Inspect DEMO-000171"
            />
            <AttentionRow
              severity="neutral"
              label="Providers without credentials"
              value="doku"
              href="/providers"
              action="Open provider catalog"
            />
          </PanelBody>
        </Panel>

        <Panel>
          <PanelHeader
            title="Payments per hour, last 24h"
            hint="Failed bars stack on top of paid and in-flight bars."
          />
          <PanelBody>
            <div className="grid gap-6 lg:grid-cols-[1fr_260px]">
              <HourlyBars paid={paymentsPerHour} failed={failedPerHour} />
              <DefList>
                <DefRow label="Total created">
                  <span className="num">{formatCount(total)}</span>
                </DefRow>
                {statusOrder.map((status) => (
                  <DefRow key={status} label={status[0].toUpperCase() + status.slice(1)}>
                    <span className="num">{formatCount(counts[status])}</span>
                  </DefRow>
                ))}
              </DefList>
            </div>
          </PanelBody>
        </Panel>

        <div className="grid gap-4 lg:grid-cols-2">
          <Panel>
            <PanelHeader
              title="Outbound delivery state, last 24h"
              hint={`Scoped to ${environment}.`}
            />
            <PanelBody className="flex flex-col gap-3">
              {deliveryStates.map((state) => (
                <div key={state.state} className="grid grid-cols-[120px_1fr_40px] items-center gap-3">
                  <span className="text-[12px] text-secondary">{state.label}</span>
                  <MiniBar
                    value={state.count}
                    max={deliveryMax}
                    tone={state.state === "failed" ? "danger" : state.state === "pending" ? "muted" : "accent"}
                  />
                  <span className="num text-right text-[13px] text-ink">
                    {state.count}
                  </span>
                </div>
              ))}
              <div className="pt-1">
                <Button asChild variant="quiet" size="sm">
                  <Link href="/webhooks">Open webhooks</Link>
                </Button>
              </div>
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader
              title="Provider reliability, last 24h"
              hint="Failed rate is failed payments divided by payments."
            />
            <table className="w-full border-collapse text-[13px]">
              <thead className="bg-surface-muted text-left">
                <tr>
                  <th className="px-4 py-2 text-[12px] font-medium text-secondary">
                    Provider
                  </th>
                  <th className="px-3 py-2 text-right text-[12px] font-medium text-secondary">
                    Payments
                  </th>
                  <th className="px-3 py-2 text-right text-[12px] font-medium text-secondary">
                    Failed
                  </th>
                  <th className="px-4 py-2 text-right text-[12px] font-medium text-secondary">
                    Failed rate
                  </th>
                </tr>
              </thead>
              <tbody>
                {providerReliability.map((row) => (
                  <tr
                    key={row.provider}
                    className="border-b border-border-subtle last:border-b-0"
                  >
                    <td className="px-4 py-2.5 font-mono text-[12.5px] text-ink">
                      {row.provider}
                    </td>
                    <td className="num px-3 py-2.5 text-right text-ink">
                      {formatCount(row.payments)}
                    </td>
                    <td className="num px-3 py-2.5 text-right text-ink">
                      {formatCount(row.failed)}
                    </td>
                    <td className="num px-4 py-2.5 text-right text-ink">
                      {((row.failed / row.payments) * 100).toFixed(1)}%
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Panel>
        </div>
      </div>
    </>
  );
}

function AttentionRow({
  severity,
  label,
  value,
  href,
  action,
}: {
  severity: "danger" | "warn" | "neutral" | "ok";
  label: string;
  value: string;
  href: string;
  action: string;
}) {
  const tone =
    severity === "danger"
      ? "danger"
      : severity === "warn"
        ? "warn"
        : severity === "ok"
          ? "ok"
          : "neutral";
  const toneLabel =
    severity === "danger"
      ? "Action"
      : severity === "warn"
        ? "Review"
        : severity === "ok"
          ? "Clear"
          : "Setup";
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border-subtle pb-3 last:border-b-0 last:pb-0">
      <div className="flex items-center gap-2.5">
        <Tag tone={tone}>{toneLabel}</Tag>
        <span className="text-[13px] text-ink">{label}</span>
        <span className="text-[13px] text-muted">{value}</span>
      </div>
      <Link
        href={href}
        className="text-[13px] font-medium text-accent underline-offset-2 hover:underline"
      >
        {action}
      </Link>
    </div>
  );
}
