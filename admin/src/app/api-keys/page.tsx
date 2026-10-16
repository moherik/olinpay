"use client";

import { useMemo, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { StatusTag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import { apiKeys as seedKeys } from "@/lib/demo-data";
import type { ApiKey } from "@/lib/demo-data";
import { formatDateTime } from "@/lib/format";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";

export default function ApiKeysPage() {
  const { environment } = useApp();
  const [keys, setKeys] = useState<ApiKey[]>(seedKeys);
  const [query, setQuery] = useState("");
  const [merchantId, setMerchantId] = useState<string>("all");
  const [revealKey, setRevealKey] = useState<ApiKey | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [label, setLabel] = useState("");
  const [createMerchant, setCreateMerchant] = useState<string>("mch_demo_a");
  const [createError, setCreateError] = useState<string | null>(null);

  const merchantOptions = useMemo(() => {
    const set = new Set(keys.map((k) => k.merchant));
    const list = Array.from(set).sort();
    return [
      { value: "all", label: "All merchants" },
      ...list.map((name) => ({ value: name, label: name })),
    ];
  }, [keys]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return keys.filter((k) => {
      if (k.environment !== environment) return false;
      if (merchantId !== "all" && k.merchant !== merchantId) return false;
      if (needle) {
        const hay = `${k.label} ${k.merchant} ${k.prefix}`.toLowerCase();
        if (!hay.includes(needle)) return false;
      }
      return true;
    });
  }, [keys, environment, merchantId, query]);

  const activeCount = filtered.filter((k) => k.status === "active").length;

  function createKey() {
    const trimmed = label.trim();
    if (trimmed.length < 2) {
      setCreateError("Enter a label of at least two characters.");
      return;
    }
    const id = `key_${Date.now().toString().slice(-6)}`;
    const prefix = `sk_${environment === "production" ? "live" : "test"}_${id.slice(-4)}`;
    setKeys((prev) => [
      {
        id,
        merchant: createMerchant,
        label: trimmed,
        prefix,
        environment,
        createdAt: new Date().toISOString(),
        lastUsedAt: null,
        status: "active",
      },
      ...prev,
    ]);
    setCreateOpen(false);
    setLabel("");
    setCreateError(null);
  }

  function revokeKey(k: ApiKey) {
    setKeys((prev) => prev.map((x) => (x.id === k.id ? { ...x, status: "revoked" } : x)));
  }

  return (
    <>
      <PageHeader
        title="API keys"
        description={`Keys scoped to the active environment (${environment}).`}
        actions={
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            Create API key
          </Button>
        }
      />

      <div className="mb-4 grid gap-3 rounded-panel border border-border bg-surface p-3 sm:grid-cols-2 lg:grid-cols-[320px_240px_auto]">
        <Field label="Search" htmlFor="keys-query" className="w-full">
          <Input
            id="keys-query"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Label, merchant, or key id"
          />
        </Field>
        <Field label="Merchant" htmlFor="keys-merchant" className="w-full">
          <SelectControl
            id="keys-merchant"
            value={merchantId}
            onValueChange={setMerchantId}
            options={merchantOptions}
            className="w-full"
          />
        </Field>
      </div>

      <Panel>
        <PanelHeader
          title="Active keys"
          hint={`${activeCount} active of ${filtered.length} in ${environment}`}
        />
        {filtered.length === 0 ? (
          <EmptyState
            title="No API keys found"
            body="Try adjusting the search or merchant filter for the current environment."
            action={
              <Button variant="secondary" onClick={() => { setQuery(""); setMerchantId("all"); }}>
                Clear filters
              </Button>
            }
          />
        ) : (
          <Table>
            <THead>
              <TR className="hover:bg-surface-muted">
                <TH>Label</TH>
                <TH>Merchant</TH>
                <TH>Key ID</TH>
                <TH>Permissions</TH>
                <TH>Status</TH>
                <TH>Created</TH>
                <TH>Last used</TH>
                <TH>
                  <span className="sr-only">Actions</span>
                </TH>
              </TR>
            </THead>
            <TBody>
              {filtered.map((k) => (
                <TR key={k.id}>
                  <TD>{k.label}</TD>
                  <TD>{k.merchant}</TD>
                  <TD className="font-mono text-[12.5px]">{k.prefix}••••</TD>
                  <TD>
                    <span className="text-[12px] text-muted">All access (demo)</span>
                  </TD>
                  <TD>
                    <StatusTag status={k.status === "active" ? "paid" : "failed"} />
                  </TD>
                  <TD className="num whitespace-nowrap text-secondary">
                    {formatDateTime(k.createdAt)}
                  </TD>
                  <TD className="num whitespace-nowrap text-secondary">
                    {k.lastUsedAt ? formatDateTime(k.lastUsedAt) : "never"}
                  </TD>
                  <TD className="text-right">
                    <div className="flex items-center justify-end gap-1">
                      <Button size="sm" variant="secondary" onClick={() => setRevealKey(k)}>
                        Reveal
                      </Button>
                      {k.status === "active" && (
                        <ConfirmDialog
                          trigger={
                            <Button size="sm" variant="danger">
                              Revoke
                            </Button>
                          }
                          title="Revoke API key?"
                          description={`Revoke "${k.label}" (${k.prefix}••••) for ${k.merchant}? This action cannot be undone.`}
                          confirmLabel="Revoke"
                          onConfirm={() => revokeKey(k)}
                        />
                      )}
                    </div>
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Panel>

      <DialogRoot open={revealKey !== null} onOpenChange={(o) => { if (!o) setRevealKey(null); }}>
        <DialogContent title="API key" description="Keep this secret. It will only be shown once in a real build.">
          {revealKey ? (
            <div className="flex flex-col gap-3">
              <Field label="Label">
                <Input readOnly value={revealKey.label} />
              </Field>
              <Field label="Prefix">
                <Input readOnly value={revealKey.prefix} />
              </Field>
              <Field label="Secret (demo)">
                <Input readOnly value={`sk_demo_${revealKey.prefix.replace("sk_", "")}`} />
              </Field>
              <p className="text-[12px] text-muted">Demo value — no real secret is shown.</p>
            </div>
          ) : null}
        </DialogContent>
      </DialogRoot>

      <DialogRoot open={createOpen} onOpenChange={(o) => { if (!o) { setCreateOpen(false); setCreateError(null); setLabel(""); } }}>
        <DialogContent
          title="Create API key"
          description={`Create a new key for the active environment (${environment}).`}
          footer={
            <>
              <Button variant="secondary" onClick={() => { setCreateOpen(false); setCreateError(null); setLabel(""); }}>
                Cancel
              </Button>
              <Button variant="primary" onClick={createKey}>
                Create
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-4">
            <Field label="Label" htmlFor="key-label" required error={createError ?? undefined}>
              <Input
                id="key-label"
                value={label}
                onChange={(e) => { setLabel(e.target.value); setCreateError(null); }}
                placeholder="e.g. Checkout, Mobile app"
              />
            </Field>
            <Field label="Merchant" htmlFor="key-merchant">
              <SelectControl
                id="key-merchant"
                value={createMerchant}
                onValueChange={setCreateMerchant}
                options={merchantOptions.filter((o) => o.value !== "all")}
                className="w-full"
              />
            </Field>
          </div>
        </DialogContent>
      </DialogRoot>
    </>
  );
}
