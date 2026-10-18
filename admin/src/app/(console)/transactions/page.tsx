"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { ApiFailed, ApiLoading } from "@/components/ui/api-state";
import { IconMore } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/ui/field";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusTag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import {
  DropdownContent,
  DropdownItem,
  DropdownRoot,
  DropdownSeparator,
  DropdownTrigger,
} from "@/components/ui/dropdown-menu";
import type { Transaction } from "@/lib/demo-data";
import {
  apiGet,
  apiMutate,
  ApiError,
  mapTransaction,
  merchantName,
  type RawTransaction,
} from "@/lib/api";
import { formatDateTime, formatIDR } from "@/lib/format";

const statusOptions: { value: string; label: string }[] = [
  { value: "all", label: "All statuses" },
  { value: "paid", label: "Paid" },
  { value: "pending", label: "Pending" },
  { value: "failed", label: "Failed" },
  { value: "expired", label: "Expired" },
  { value: "cancelled", label: "Cancelled" },
  { value: "refunded", label: "Refunded" },
  { value: "unknown", label: "Unknown" },
];

const PAGE_SIZE = 50;

function csvCell(value: string) {
  return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
}

export default function TransactionsPage() {
  const router = useRouter();
  const {
    environment,
    notify,
    auth,
    merchants,
    can,
  } = useApp();
  const [rows, setRows] = useState<Transaction[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [queryInput, setQueryInput] = useState("");
  const [query, setQuery] = useState("");
  const [merchant, setMerchant] = useState("all");
  const [status, setStatus] = useState("all");
  const [provider, setProvider] = useState("all");
  const [cancelTarget, setCancelTarget] = useState<string | null>(null);
  const [cancelReason, setCancelReason] = useState("");
  const [cancelling, setCancelling] = useState(false);

  // Search text is debounced into the actual query so typing does not fire
  // one request per keystroke.
  useEffect(() => {
    const timer = setTimeout(() => setQuery(queryInput), 300);
    return () => clearTimeout(timer);
  }, [queryInput]);

  const merchantOptions = useMemo(
    () => [
      { value: "all", label: "All merchants" },
      ...merchants.map((entry) => ({
        value: entry.id,
        label: entry.name,
      })),
    ],
    [merchants],
  );

  const load = useCallback(
    async (offset: number, append: boolean) => {
      setLoading(true);
      setError(null);
      try {
        const { data, meta } = await apiGet<RawTransaction[]>(
          "/transactions",
          {
            env: environment,
            q: query,
            merchant_id: merchant === "all" ? undefined : merchant,
            status: status === "all" ? undefined : status,
            provider: provider === "all" ? undefined : provider,
            limit: PAGE_SIZE,
            offset,
          },
        );
        const mapped = data.map(mapTransaction);
        setRows((current) =>
          append ? [...current, ...mapped] : mapped,
        );
        setHasMore(meta.has_more === true);
      } catch (cause) {
        setError(
          cause instanceof ApiError
            ? cause.message
            : "Cannot load transactions.",
        );
      } finally {
        setLoading(false);
      }
    },
    [environment, query, merchant, status, provider],
  );

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(0, false), 0);
    return () => clearTimeout(timer);
  }, [load]);

  // Provider options come from the loaded rows; the filter list grows with
  // real traffic instead of hardcoding gateway names.
  const providerOptions = useMemo(() => {
    const names = Array.from(new Set(rows.map((row) => row.provider))).sort();
    return [
      { value: "all", label: "All providers" },
      ...names.map((name) => ({ value: name, label: name })),
    ];
  }, [rows]);

  const filtersActive =
    merchant !== "all" || status !== "all" || provider !== "all" || query !== "";

  function clearFilters() {
    setMerchant("all");
    setStatus("all");
    setProvider("all");
    setQueryInput("");
  }

  function exportCsv() {
    const header = [
      "reference",
      "transaction_id",
      "merchant_id",
      "environment",
      "provider",
      "method",
      "channel",
      "amount",
      "currency",
      "status",
      "created_at",
    ];
    const lines = rows.map((row) =>
      [
        row.reference,
        row.id,
        row.merchant,
        row.environment,
        row.provider,
        row.method,
        row.channel,
        String(row.amount),
        row.currency,
        row.status,
        row.createdAt,
      ]
        .map(csvCell)
        .join(","),
    );
    const csv = [header.join(","), ...lines].join("\n");
    const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "openpay-transactions.csv";
    anchor.click();
    URL.revokeObjectURL(url);
    notify(`Exported ${rows.length} rows to CSV.`);
  }

  const canCancel =
    can("cancel") &&
    auth.ready &&
    auth.authenticated &&
    auth.user.role !== "tech";

  const target = rows.find((row) => row.id === cancelTarget);

  async function confirmCancel() {
    if (!cancelTarget) return;
    setCancelling(true);
    try {
      await apiMutate("POST", `/transactions/${cancelTarget}/cancel`, {
        reason: cancelReason.trim(),
      });
      notify(
        `Cancellation recorded for ${target?.reference ?? cancelTarget}${
          cancelReason.trim() ? `. Reason: ${cancelReason.trim()}.` : "."
        }`,
      );
      setCancelTarget(null);
      setCancelReason("");
      void load(0, false);
    } catch (cause) {
      notify(
        cause instanceof ApiError
          ? `Cancel failed: ${cause.message}`
          : "Cancel failed — the gateway did not accept it.",
      );
    } finally {
      setCancelling(false);
    }
  }

  return (
    <>
      <PageHeader
        title="Transactions"
        description={`Every payment the platform created in ${environment}. Filters run server-side; cancel writes to the gateway.`}
        actions={
          <Button
            variant="secondary"
            onClick={exportCsv}
            disabled={rows.length === 0}
          >
            Export CSV
          </Button>
        }
      />

      <div className="mb-4 flex flex-wrap items-end gap-3 rounded-panel border border-border bg-surface p-3">
        <Field label="Reference search" htmlFor="trx-query" className="w-[240px]">
          <Input
            id="trx-query"
            value={queryInput}
            onChange={(event) => setQueryInput(event.target.value)}
            placeholder="Exact reference_id"
          />
        </Field>
        <Field label="Merchant" className="w-[180px]">
          <SelectControl
            ariaLabel="Filter by merchant"
            value={merchant}
            onValueChange={(value) => {
              setMerchant(value);
            }}
            options={merchantOptions}
            className="w-full"
          />
        </Field>
        <Field label="Status" className="w-[150px]">
          <SelectControl
            ariaLabel="Filter by status"
            value={status}
            onValueChange={setStatus}
            options={statusOptions}
            className="w-full"
          />
        </Field>
        <Field label="Provider" className="w-[170px]">
          <SelectControl
            ariaLabel="Filter by provider"
            value={provider}
            onValueChange={setProvider}
            options={providerOptions}
            className="w-full"
          />
        </Field>
        {filtersActive ? (
          <Button variant="ghost" onClick={clearFilters}>
            Clear filters
          </Button>
        ) : null}
      </div>

      {error && rows.length === 0 ? (
        <ApiFailed
          title="Transactions unavailable"
          error={error}
          onRetry={() => void load(0, false)}
        />
      ) : (
        <Panel>
          <PanelHeader
            title="All transactions"
            hint={`Exact-match reference search in ${environment}`}
          />
          {loading && rows.length === 0 ? (
            <ApiLoading label="Loading transactions…" />
          ) : !loading && rows.length === 0 ? (
            <EmptyState
              title="No transactions match these filters"
              body="Nothing in this environment matches the current reference, merchant, status and provider. Clear the filters to see the full list."
              action={
                filtersActive ? (
                  <Button variant="secondary" onClick={clearFilters}>
                    Clear filters
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <>
              <Table>
                <THead>
                  <TR className="hover:bg-surface-muted">
                    <TH>Reference</TH>
                    <TH>Merchant</TH>
                    <TH>Provider</TH>
                    <TH>Method</TH>
                    <TH className="text-right">Amount</TH>
                    <TH>Status</TH>
                    <TH>Created</TH>
                    <TH>
                      <span className="sr-only">Row actions</span>
                    </TH>
                  </TR>
                </THead>
                <TBody>
                  {rows.map((row) => (
                    <TR key={row.id}>
                      <TD>
                        <Link
                          href={`/transactions/${row.id}`}
                          className="font-mono text-[12.5px] text-accent underline-offset-2 hover:underline"
                        >
                          {row.reference}
                        </Link>
                      </TD>
                      <TD>{merchantName(row.merchant, merchants)}</TD>
                      <TD className="font-mono text-[12.5px]">{row.provider}</TD>
                      <TD>
                        <span className="text-ink">{row.method}</span>
                        {row.channel && <span className="text-muted"> / {row.channel}</span>}
                      </TD>
                      <TD className="num text-right">{formatIDR(row.amount)}</TD>
                      <TD>
                        <StatusTag status={row.status} />
                      </TD>
                      <TD className="num whitespace-nowrap text-secondary">
                        {formatDateTime(row.createdAt)}
                      </TD>
                      <TD className="text-right">
                        <DropdownRoot>
                          <DropdownTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              aria-label={`Actions for ${row.reference}`}
                            >
                              <IconMore size={16} />
                            </Button>
                          </DropdownTrigger>
                          <DropdownContent>
                            <DropdownItem
                              onSelect={() => router.push(`/transactions/${row.id}`)}
                            >
                              View details
                            </DropdownItem>
                            <DropdownItem
                              onSelect={() => {
                                void navigator.clipboard?.writeText(row.id);
                                notify(`Copied ${row.id} to clipboard.`);
                              }}
                            >
                              Copy transaction id
                            </DropdownItem>
                            <DropdownSeparator />
                            <DropdownItem
                              tone="danger"
                              disabled={row.status !== "pending" || !canCancel}
                              onSelect={() => setCancelTarget(row.id)}
                            >
                              {!canCancel
                                ? "Cancel payment (needs operator role)"
                                : row.status === "pending"
                                  ? "Cancel payment"
                                  : "Cancel payment (pending only)"}
                            </DropdownItem>
                          </DropdownContent>
                        </DropdownRoot>
                      </TD>
                    </TR>
                  ))}
                </TBody>
              </Table>
              <div className="flex justify-center border-t border-border-subtle p-3">
                {hasMore ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={loading}
                    onClick={() => void load(rows.length, true)}
                  >
                    {loading ? "Loading…" : "Load more"}
                  </Button>
                ) : (
                  <span className="text-[12px] text-muted">
                    End of list — {rows.length} rows
                  </span>
                )}
              </div>
            </>
          )}
        </Panel>
      )}

      <DialogRoot
        open={cancelTarget !== null}
        onOpenChange={(open) => {
          if (!open) {
            setCancelTarget(null);
            setCancelReason("");
          }
        }}
      >
        <DialogContent
          title={`Cancel ${target?.reference ?? "payment"}?`}
          description="Cancelling tells the gateway to void the payment. A pending payment can still be paid until the gateway confirms."
          footer={
            <>
              <Button
                variant="secondary"
                onClick={() => {
                  setCancelTarget(null);
                  setCancelReason("");
                }}
              >
                Keep payment
              </Button>
              <Button
                variant="primary"
                disabled={cancelling}
                onClick={() => void confirmCancel()}
              >
                {cancelling ? "Cancelling…" : "Cancel payment"}
              </Button>
            </>
          }
        >
          <Field
            label="Reason"
            htmlFor="cancel-reason"
            help="Recorded in the audit log against your account."
          >
            <Input
              id="cancel-reason"
              value={cancelReason}
              onChange={(event) => setCancelReason(event.target.value)}
              placeholder="Customer requested cancellation"
            />
          </Field>
        </DialogContent>
      </DialogRoot>
    </>
  );
}
