"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { ApiFailed, ApiLoading } from "@/components/ui/api-state";
import { CodeBlock } from "@/components/ui/code";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { Tag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import type { ApiKey } from "@/lib/demo-data";
import {
  apiGet,
  apiMutate,
  ApiError,
  mapApiKey,
  merchantName,
  type RawApiKey,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";

export default function ApiKeysPage() {
  const { environment, merchants, notify, auth } = useApp();
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [merchantId, setMerchantId] = useState("all");

  const [createOpen, setCreateOpen] = useState(false);
  const [label, setLabel] = useState("");
  const [createMerchant, setCreateMerchant] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [revealed, setRevealed] = useState<string | null>(null);

  const isOwner = auth.ready && auth.authenticated && auth.user.role === "owner";

  const load = useCallback(async () => {
    setError(null);
    try {
      const { data } = await apiGet<RawApiKey[]>("/api-keys", {
        env: environment,
        merchant_id: merchantId === "all" ? undefined : merchantId,
        limit: 100,
      });
      setKeys(data.map(mapApiKey));
      setLoaded(true);
    } catch (cause) {
      setError(
        cause instanceof ApiError ? cause.message : "Cannot load API keys.",
      );
    }
  }, [environment, merchantId]);

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(), 0);
    return () => clearTimeout(timer);
  }, [load]);

  const merchantOptions = useMemo(
    () => [
      { value: "all", label: "All merchants" },
      ...merchants.map((merchant) => ({
        value: merchant.id,
        label: merchant.name,
      })),
    ],
    [merchants],
  );

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return keys;
    return keys.filter((key) =>
      `${key.label} ${key.merchant} ${key.prefix} ${merchantName(key.merchant, merchants)}`
        .toLowerCase()
        .includes(needle),
    );
  }, [keys, query, merchants]);

  const activeCount = filtered.filter((key) => key.status === "active").length;

  async function createKey() {
    const trimmed = label.trim();
    if (trimmed.length < 2) {
      setCreateError("Enter a label of at least two characters.");
      return;
    }
    if (createMerchant === "") {
      setCreateError("Choose the merchant this key belongs to.");
      return;
    }
    setBusy(true);
    setCreateError(null);
    try {
      const { data, meta } = await apiMutate<RawApiKey>("POST", "/api-keys", {
        merchant_id: createMerchant,
        label: trimmed,
        environment,
      });
      setRevealed(typeof meta.key === "string" ? meta.key : null);
      setCreateOpen(false);
      setLabel("");
      notify(`API key created for ${merchantName(createMerchant, merchants)}.`);
      if (data) {
        setKeys((current) => [mapApiKey(data), ...current]);
      }
    } catch (cause) {
      setCreateError(
        cause instanceof ApiError ? cause.message : "Could not create the key.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function revokeKey(key: ApiKey) {
    try {
      await apiMutate("POST", `/api-keys/${key.id}/revoke`);
      setKeys((current) =>
        current.map((entry) =>
          entry.id === key.id ? { ...entry, status: "revoked" as const } : entry,
        ),
      );
      notify(
        `Revoked ${key.prefix}…. Signed-in integrations start failing with 401.`,
      );
    } catch (cause) {
      notify(
        cause instanceof ApiError ? cause.message : "Revoke failed.",
      );
    }
  }

  return (
    <>
      <PageHeader
        title="API keys"
        description={`Merchant credentials scoped to ${environment}. Keys authenticate the payments API; the environment comes from the key itself (sk_test_/sk_live_).`}
        actions={
          isOwner && merchants.length > 0 ? (
            <Button variant="primary" onClick={() => setCreateOpen(true)}>
              Create API key
            </Button>
          ) : null
        }
      />

      <div className="mb-4 grid gap-3 rounded-panel border border-border bg-surface p-3 sm:grid-cols-2 lg:grid-cols-[320px_240px_auto]">
        <Field label="Search" htmlFor="keys-query" className="w-full">
          <Input
            id="keys-query"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Label, merchant, or key prefix"
          />
        </Field>
        <Field label="Merchant" htmlFor="keys-merchant" className="w-full">
          <SelectControl
            id="keys-merchant"
            ariaLabel="Filter by merchant"
            value={merchantId}
            onValueChange={setMerchantId}
            options={merchantOptions}
            className="w-full"
          />
        </Field>
      </div>

      <Panel>
        <PanelHeader
          title={`${environment} keys`}
          hint={`${activeCount} active of ${filtered.length} in ${environment}`}
        />
        {error && keys.length === 0 ? (
          <ApiFailed
            title="API keys unavailable"
            error={error}
            onRetry={() => void load()}
          />
        ) : !loaded ? (
          <ApiLoading label="Loading keys…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            title="No API keys found"
            body={
              keys.length === 0
                ? "This environment has no keys yet. Create one from the merchant's page or here."
                : "Try adjusting the search or merchant filter."
            }
            action={
              <Button
                variant="secondary"
                onClick={() => {
                  setQuery("");
                  setMerchantId("all");
                }}
              >
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
                <TH>Prefix</TH>
                <TH>Status</TH>
                <TH>Created</TH>
                <TH>Last used</TH>
                <TH>
                  <span className="sr-only">Actions</span>
                </TH>
              </TR>
            </THead>
            <TBody>
              {filtered.map((key) => (
                <TR key={key.id}>
                  <TD>{key.label}</TD>
                  <TD>{merchantName(key.merchant, merchants)}</TD>
                  <TD className="font-mono text-[12.5px]">{key.prefix}…</TD>
                  <TD>
                    <Tag tone={key.status === "active" ? "ok" : "neutral"}>
                      {key.status}
                    </Tag>
                  </TD>
                  <TD className="num whitespace-nowrap text-secondary">
                    {formatDateTime(key.createdAt)}
                  </TD>
                  <TD className="num whitespace-nowrap text-secondary">
                    {key.lastUsedAt ? formatDateTime(key.lastUsedAt) : "never"}
                  </TD>
                  <TD className="text-right">
                    {key.status === "active" ? (
                      isOwner ? (
                        <ConfirmDialog
                          trigger={
                            <Button size="sm" variant="danger">
                              Revoke
                            </Button>
                          }
                          title="Revoke API key?"
                          description={`Revoke "${key.label}" (${key.prefix}…) for ${merchantName(key.merchant, merchants)}? Integrations using it start failing with 401 immediately.`}
                          confirmLabel="Revoke"
                          onConfirm={() => void revokeKey(key)}
                        />
                      ) : (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled
                          title="Only the owner role can revoke keys."
                        >
                          Revoke (owner)
                        </Button>
                      )
                    ) : (
                      <span className="text-[12px] text-muted">&mdash;</span>
                    )}
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Panel>

      <DialogRoot
        open={revealed !== null}
        onOpenChange={(open) => {
          if (!open) setRevealed(null);
        }}
      >
        <DialogContent
          title="Store the key now"
          description="The platform stores only a salted hash and this key's prefix. It cannot be shown again."
          footer={
            <Button variant="primary" onClick={() => setRevealed(null)}>
              I have copied it
            </Button>
          }
        >
          <CodeBlock label="api_key.txt" code={revealed ?? ""} />
        </DialogContent>
      </DialogRoot>

      <DialogRoot
        open={createOpen}
        onOpenChange={(open) => {
          if (!open) {
            setCreateOpen(false);
            setCreateError(null);
            setLabel("");
          }
        }}
      >
        <DialogContent
          title="Create API key"
          description={`Create a new key for ${environment}. The merchant decides which integration uses it.`}
          footer={
            <>
              <Button
                variant="secondary"
                onClick={() => {
                  setCreateOpen(false);
                  setCreateError(null);
                  setLabel("");
                }}
              >
                Cancel
              </Button>
              <Button
                variant="primary"
                disabled={busy}
                onClick={() => void createKey()}
              >
                {busy ? "Creating…" : "Create key"}
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-4">
            <Field
              label="Label"
              htmlFor="key-label"
              required
              error={createError ?? undefined}
            >
              <Input
                id="key-label"
                value={label}
                onChange={(event) => {
                  setLabel(event.target.value);
                  setCreateError(null);
                }}
                placeholder="Checkout, Mobile app"
              />
            </Field>
            <Field label="Merchant" htmlFor="key-merchant">
              <SelectControl
                id="key-merchant"
                ariaLabel="Key merchant"
                value={createMerchant === "" ? "none" : createMerchant}
                onValueChange={(value) => {
                  if (value === "none") return;
                  setCreateMerchant(value);
                  setCreateError(null);
                }}
                options={
                  merchants.length > 0
                    ? [
                        { value: "none", label: "Choose a merchant" },
                        ...merchants.map((merchant) => ({
                          value: merchant.id,
                          label: merchant.name,
                        })),
                      ]
                    : [{ value: "none", label: "No merchants yet" }]
                }
                className="w-full"
              />
            </Field>
          </div>
        </DialogContent>
      </DialogRoot>
    </>
  );
}
