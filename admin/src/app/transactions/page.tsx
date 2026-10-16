"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useMemo, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
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

function csvCell(value: string) {
  return /[",\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value;
}

export default function TransactionsPage() {
  const router = useRouter();
  const { environment, notify, transactions, updateTransaction } = useApp();
  const [query, setQuery] = useState("");
  const [merchant, setMerchant] = useState("all");
  const [status, setStatus] = useState("all");
  const [provider, setProvider] = useState("all");
  const [cancelTarget, setCancelTarget] = useState<string | null>(null);
  const [cancelReason, setCancelReason] = useState("");

  const merchantOptions = useMemo(() => {
    const names = Array.from(
      new Set(transactions.map((row) => row.merchant)),
    ).sort();
    return [
      { value: "all", label: "All merchants" },
      ...names.map((name) => ({ value: name, label: name })),
    ];
  }, [transactions]);

  const providerOptions = useMemo(() => {
    const names = Array.from(
      new Set(transactions.map((row) => row.provider)),
    ).sort();
    return [
      { value: "all", label: "All providers" },
      ...names.map((name) => ({ value: name, label: name })),
    ];
  }, [transactions]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return transactions.filter((row) => {
      if (row.environment !== environment) return false;
      if (merchant !== "all" && row.merchant !== merchant) return false;
      if (status !== "all" && row.status !== status) return false;
      if (provider !== "all" && row.provider !== provider) return false;
      if (
        needle &&
        !row.reference.toLowerCase().includes(needle) &&
        !row.merchant.toLowerCase().includes(needle) &&
        !row.providerReference.toLowerCase().includes(needle)
      ) {
        return false;
      }
      return true;
    });
  }, [transactions, environment, merchant, status, provider, query]);

  const filtersActive =
    merchant !== "all" || status !== "all" || provider !== "all" || query !== "";

  function clearFilters() {
    setMerchant("all");
    setStatus("all");
    setProvider("all");
    setQuery("");
  }

  function exportCsv() {
    const header = [
      "reference",
      "merchant",
      "environment",
      "provider",
      "method",
      "channel",
      "amount",
      "currency",
      "status",
      "created_at",
    ];
    const lines = filtered.map((row) =>
      [
        row.reference,
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
    anchor.download = "openpay-demo-transactions.csv";
    anchor.click();
    URL.revokeObjectURL(url);
    notify(`Exported ${filtered.length} rows to CSV.`);
  }

  function confirmCancel() {
    if (!cancelTarget) return;
    updateTransaction(cancelTarget, { status: "cancelled" });
    notify(
      `Cancellation recorded for ${
        transactions.find((row) => row.id === cancelTarget)?.reference ??
        cancelTarget
      }. Reason: ${cancelReason.trim() || "not provided"}.`,
    );
    setCancelTarget(null);
    setCancelReason("");
  }

  const target = transactions.find((row) => row.id === cancelTarget);

  return (
    <>
      <PageHeader
        title="Transactions"
        description="Every payment the platform created, scoped to the environment selected above. Filters and the cancel action work locally in this demo build."
        actions={
          <Button
            variant="secondary"
            onClick={exportCsv}
            disabled={filtered.length === 0}
          >
            Export CSV
          </Button>
        }
      />

      <div className="mb-4 flex flex-wrap items-end gap-3 rounded-panel border border-border bg-surface p-3">
        <Field label="Reference or merchant" htmlFor="trx-query" className="w-[240px]">
          <Input
            id="trx-query"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="DEMO-000181"
          />
        </Field>
        <Field label="Merchant" className="w-[180px]">
          <SelectControl
            ariaLabel="Filter by merchant"
            value={merchant}
            onValueChange={setMerchant}
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

      <Panel>
        <PanelHeader
          title="All transactions"
          hint={`${filtered.length} of ${transactions.length} rows in ${environment}`}
        />
        {filtered.length === 0 ? (
          <EmptyState
            title="No transactions match these filters"
            body="Nothing in this environment matches the current reference, merchant, status and provider. Clear the filters to see the full list."
            action={
              <Button variant="secondary" onClick={clearFilters}>
                Clear filters
              </Button>
            }
          />
        ) : (
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
              {filtered.map((row) => (
                <TR key={row.id}>
                  <TD>
                    <Link
                      href={`/transactions/${row.id}`}
                      className="font-mono text-[12.5px] text-accent underline-offset-2 hover:underline"
                    >
                      {row.reference}
                    </Link>
                  </TD>
                  <TD>{row.merchant}</TD>
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
                          disabled={row.status !== "pending"}
                          onSelect={() => setCancelTarget(row.id)}
                        >
                          {row.status === "pending"
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
        )}
      </Panel>

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
              <Button variant="primary" onClick={confirmCancel}>
                Cancel payment
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
