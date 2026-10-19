"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { HourlyBars, MiniBar } from "@/components/charts";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { DefList, DefRow } from "@/components/kv";
import { ApiFailed, ApiLoading, NoData } from "@/components/ui/api-state";
import { Panel, PanelBody, PanelHeader } from "@/components/ui/panel";
import { Tag } from "@/components/ui/status-tag";
import { Button } from "@/components/ui/button";
import type { Status } from "@/lib/demo-data";
import { apiGet, ApiError, type RawOverview } from "@/lib/api";
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
  const [overview, setOverview] = useState<RawOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    apiGet<RawOverview>("/overview", { env: environment }).then(
      ({ data }) => {
        if (!cancelled) {
          setOverview(data);
          setError(null);
        }
      },
      (cause) => {
        if (!cancelled) {
          setOverview(null);
          setError(
            cause instanceof ApiError
              ? cause.message
              : "Cannot load the overview.",
          );
        }
      },
    );
    return () => {
      cancelled = true;
    };
  }, [environment, attempt]);

  if (overview === null) {
    return (
      <>
        <PageHeader
          title="Overview"
          description={`Platform totals for ${environment}.`}
        />
        {error ? (
          <ApiFailed
            title="Overview unavailable"
            error={error}
            onRetry={() => {
              setOverview(null);
              setAttempt((current) => current + 1);
            }}
          />
        ) : (
          <ApiLoading label={`Loading ${environment} overview…`} />
        )}
      </>
    );
  }

  const counts = Object.fromEntries(
    overview.statuses.map((row) => [row.status, row.count]),
  ) as Record<Status, number>;
  const total = overview.statuses.reduce((sum, row) => sum + row.count, 0);
  const chartedTotal = overview.created_per_hour.reduce(
    (sum, count) => sum + count,
    0,
  );

  const deliveryStates = [
    { state: "delivered", label: "Delivered" },
    { state: "pending", label: "Pending retry" },
    { state: "failed", label: "Failed" },
  ].map((entry) => ({
    label: entry.label,
    state: entry.state,
    count:
      entry.state === "delivered"
        ? overview.deliveries.delivered
        : entry.state === "pending"
          ? overview.deliveries.pending
          : overview.deliveries.failed,
  }));
  const deliveryMax = Math.max(...deliveryStates.map((s) => s.count), 1);

  const unknown = overview.attention.unknown_transactions;

  return (
    <>
      <PageHeader
        title="Overview"
        description={`Live platform data for ${environment}.`}
      />

      <div className="grid gap-4">
        {unknown.length > 0 ? (
          <Panel>
            <PanelHeader
              title="Needs attention"
              hint="Items an operator would act on next."
            />
            <PanelBody className="flex flex-col gap-3">
              {unknown.slice(0, 3).map((row) => (
                <AttentionRow
                  key={row.id}
                  severity="warn"
                  label="Transaction awaiting reconciliation"
                  value={`${row.reference} — outcome unknown`}
                  href={`/transactions/${row.id}`}
                  action="Inspect payment"
                />
              ))}
            </PanelBody>
          </Panel>
        ) : <></>}

        <Panel>
          <PanelHeader
            title="Payments per hour, last 24h"
            hint="Failed bars stack on top of paid and in-flight bars."
          />
          <PanelBody>
            <div className="grid gap-6 lg:grid-cols-[1fr_260px]">
              <HourlyBars
                paid={overview.created_per_hour.map(
                  (created, index) =>
                    Math.max(0, created - (overview.failed_per_hour[index] ?? 0)),
                )}
                failed={overview.failed_per_hour}
              />
              <DefList>
                <DefRow label="Created, last 24h">
                  <span className="num">{formatCount(chartedTotal)}</span>
                </DefRow>
                <DefRow label="Status breakdown, all time">
                  <span className="num">{formatCount(total)}</span>
                </DefRow>
                {statusOrder.map((status) => (
                  <DefRow key={status} label={status[0].toUpperCase() + status.slice(1)}>
                    <span className="num">{formatCount(counts[status] ?? 0)}</span>
                  </DefRow>
                ))}
              </DefList>
            </div>
          </PanelBody>
        </Panel>

        <div className="grid gap-4 lg:grid-cols-2">
          <Panel>
            <PanelHeader
              title="Outbound delivery state, all time"
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
              title="Provider reliability"
              hint={`Failed rate = failed payments / payments, in ${environment}.`}
            />
            {overview.providers.length === 0 ? (
              <NoData
                title="No payments yet"
                body="Provider reliability appears once transactions exist."
              />
            ) : (
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
                  {overview.providers.map((row) => (
                    <tr
                      key={row.provider}
                      className="border-b border-border-subtle hover:bg-surface-muted/60 last:border-b-0"
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
                        {row.payments > 0
                          ? `${((row.failed / row.payments) * 100).toFixed(1)}%`
                          : "—"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
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
      <Button asChild variant="quiet" size="sm">
        <Link href={href}>{action}</Link>
      </Button>
    </div>
  );
}
