"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { Suspense, useCallback, useEffect, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { DefList, DefRow, Mono } from "@/components/kv";
import { ApiFailed, ApiLoading } from "@/components/ui/api-state";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelBody, PanelHeader } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { Tag, StatusTag } from "@/components/ui/status-tag";
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
import type { ApiKey, Environment, Transaction } from "@/lib/demo-data";
import {
  apiGet,
  apiMutate,
  ApiError,
  mapApiKey,
  mapTransaction,
  type RawApiKey,
  type RawProviderEntry,
  type RawTransaction,
} from "@/lib/api";
import { PriorityEditor } from "@/components/routing/priority-editor";
import { formatDateTime } from "@/lib/format";

/**
 * Routing entries as stored by the engine. Sandbox provider instances are
 * suffixed ".sandbox"; everything else aims at production.
 */
type RoutingEntry = { name: string; enabled: boolean; priority: number };

type RawMerchantDetail = {
  id: string;
  name: string;
  callback_url: string;
  status: "active" | "inactive" | "suspended";
  created_at: string;
  api_key_prefix: string;
  routing: { strategy: string; providers: RoutingEntry[] };
};

export default function MerchantDetailPage() {
  return (
    <Suspense fallback={<DetailFallback />}>
      <MerchantDetailInner />
    </Suspense>
  );
}

function DetailFallback() {
  return (
    <>
      <PageHeader title="Merchant" description="Loading this merchant." />
      <Panel>
        <ApiLoading label="Loading merchant" />
      </Panel>
    </>
  );
}

function MerchantDetailInner() {
  const params = useParams<{ id: string }>();
  const { notify, environment, merchants, reloadMerchants, auth } =
    useApp();

  const [merchant, setMerchant] = useState<RawMerchantDetail | null>(null);
  const [environments, setEnvironments] = useState<string[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | null>(null);
  const [providers, setProviders] = useState<RawProviderEntry[]>([]);
  const [callbackDraft, setCallbackDraft] = useState("");
  const [savingCallback, setSavingCallback] = useState(false);
  const [returnURLDraft, setReturnURLDraft] = useState("");
  const [successReturnURLDraft, setSuccessReturnURLDraft] = useState("");
  const [failureReturnURLDraft, setFailureReturnURLDraft] = useState("");
  const [savingReturnURLs, setSavingReturnURLs] = useState(false);

  const load = useCallback(async () => {
    setState("loading");
    setError(null);
    try {
      const { data, meta } = await apiGet<RawMerchantDetail>(
        `/merchants/${params.id}`,
      );
      setMerchant(data);
      setCallbackDraft(data.callback_url);
      const { data: settings } = await apiGet<{
        return_url: string;
        success_return_url: string;
        failure_return_url: string;
      }>(`/merchants/${params.id}/settings`);
      setReturnURLDraft(settings.return_url || "");
      setSuccessReturnURLDraft(settings.success_return_url || "");
      setFailureReturnURLDraft(settings.failure_return_url || "");
      setEnvironments(
        Array.isArray(meta.environments)
          ? (meta.environments as string[])
          : [],
      );
      try {
        const { data: catalog } = await apiGet<RawProviderEntry[]>("/providers");
        setProviders(catalog);
      } catch {
        setProviders([]);
      }
      setState("ready");
    } catch (cause) {
      setState("error");
      setError(
        cause instanceof ApiError ? cause.message : "Cannot load merchant.",
      );
    }
  }, [params.id]);

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(), 0);
    return () => clearTimeout(timer);
  }, [load]);

  if (state === "loading" && !merchant) {
    return (
      <>
        <PageHeader title="Merchant" description="Loading this merchant." />
        <Panel>
          <ApiLoading label="Loading merchant" />
        </Panel>
      </>
    );
  }

  if (state === "error" || !merchant) {
    return (
      <>
        <PageHeader title="Merchant unavailable" />
        <Panel>
          {error === "Merchant not found" ? (
            <EmptyState
              title="No merchant with that id"
              body="The id in the address does not match a merchant on this platform. It may have been mistyped, or the merchant may have been removed."
              action={
                <Button asChild variant="secondary">
                  <Link href="/merchants">Back to merchants</Link>
                </Button>
              }
            />
          ) : (
            <ApiFailed
              title="Merchant could not be loaded"
              error={error}
              onRetry={() => void load()}
            />
          )}
        </Panel>
      </>
    );
  }

  const stats = merchants.find((row) => row.id === merchant.id);
  const canManage =
    auth.ready && auth.authenticated && auth.user.role !== "tech";

  async function saveCallback() {
    if (!merchant) return;
    setSavingCallback(true);
    try {
      await apiMutate("PATCH", `/merchants/${merchant.id}`, {
        callback_url: callbackDraft.trim(),
      });
      notify("Callback URL updated. Outbound events now post here.");
      void reloadMerchants();
    } catch (cause) {
      notify(
        cause instanceof ApiError
          ? `Update failed: ${cause.message}`
          : "Update failed.",
      );
    } finally {
      setSavingCallback(false);
    }
  }

  async function saveReturnURLs() {
    if (!merchant) return;
    setSavingReturnURLs(true);
    try {
      await apiMutate("PUT", `/merchants/${merchant.id}/settings`, {
        return_url: returnURLDraft.trim(),
        success_return_url: successReturnURLDraft.trim(),
        failure_return_url: failureReturnURLDraft.trim(),
      });
      notify("Payer return URLs updated.");
    } catch (cause) {
      notify(cause instanceof ApiError ? `Update failed: ${cause.message}` : "Update failed.");
    } finally {
      setSavingReturnURLs(false);
    }
  }

  return (
    <>
      <PageHeader
        title={merchant.name}
        meta={
          <Tag tone={merchant.status === "active" ? "ok" : "warn"}>
            {merchant.status}
          </Tag>
        }
        description={`Merchant ${merchant.id}. Routing and keys are held per merchant; provider credentials are global per environment.`}
        actions={
          <Button asChild variant="ghost">
            <Link href="/merchants">Back to list</Link>
          </Button>
        }
      />

      <TabsRoot defaultValue="overview">
        <div className="mb-4">
          <TabsList>
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="providers">Provider routing</TabsTrigger>
            <TabsTrigger value="keys">API keys</TabsTrigger>
            <TabsTrigger value="transactions">Transactions</TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="overview">
          <div className="grid gap-4 lg:grid-cols-2">
            <Panel>
              <PanelHeader title="Merchant" />
              <PanelBody>
                <DefList>
                  <DefRow label="Id">
                    <Mono>{merchant.id}</Mono>
                  </DefRow>
                  <DefRow label="Created">
                    <span className="num">
                      {formatDateTime(merchant.created_at)}
                    </span>
                  </DefRow>
                  <DefRow label="Key environments">
                    <span className="flex flex-wrap gap-1">
                      {environments.length === 0 ? (
                        <span className="text-[12px] text-muted">no keys yet</span>
                      ) : (
                        environments.map((env) => (
                          <Tag key={env} tone="neutral">
                            {env}
                          </Tag>
                        ))
                      )}
                    </span>
                  </DefRow>
                  <DefRow label="Active keys">
                    {stats ? `${stats.keys} active` : "—"}
                  </DefRow>
                  <DefRow label="Transactions, 30d">
                    {stats ? stats.transactions30d : "—"}
                  </DefRow>
                </DefList>
              </PanelBody>
            </Panel>

            <Panel>
              <PanelHeader
                title="Callback endpoint"
                hint="The platform posts payment events here for every environment."
              />
              <PanelBody>
                <DefRow label="POST target">
                  <Mono>{merchant.callback_url || "not set"}</Mono>
                </DefRow>
                {canManage ? (
                  <div className="mt-2 flex items-end gap-2">
                    <Field
                      label="Update"
                      htmlFor="merchant-callback"
                      className="w-[300px]"
                    >
                      <Input
                        id="merchant-callback"
                        value={callbackDraft}
                        onChange={(event) =>
                          setCallbackDraft(event.target.value)
                        }
                        placeholder="https://merchant.example/webhooks"
                      />
                    </Field>
                    <Button
                      variant="secondary"
                      disabled={
                        savingCallback ||
                        callbackDraft.trim() === merchant.callback_url
                      }
                      onClick={() => void saveCallback()}
                    >
                      {savingCallback ? "Saving…" : "Save URL"}
                    </Button>
                  </div>
                ) : null}
              </PanelBody>
            </Panel>

            <Panel>
              <PanelHeader
                title="Payer return URLs"
                hint="Used for e-wallet redirects when a payment request does not provide its own URL."
              />
              <PanelBody className="grid gap-3">
                <Field label="Default return URL" htmlFor="merchant-return-url">
                  <Input
                    id="merchant-return-url"
                    type="url"
                    disabled={!canManage}
                    value={returnURLDraft}
                    onChange={(event) => setReturnURLDraft(event.target.value)}
                    placeholder="https://merchant.example/payment/return"
                  />
                </Field>
                <Field label="Success return URL" htmlFor="merchant-success-return-url">
                  <Input
                    id="merchant-success-return-url"
                    type="url"
                    disabled={!canManage}
                    value={successReturnURLDraft}
                    onChange={(event) => setSuccessReturnURLDraft(event.target.value)}
                    placeholder="Uses the default return URL when empty"
                  />
                </Field>
                <Field label="Failure return URL" htmlFor="merchant-failure-return-url">
                  <Input
                    id="merchant-failure-return-url"
                    type="url"
                    disabled={!canManage}
                    value={failureReturnURLDraft}
                    onChange={(event) => setFailureReturnURLDraft(event.target.value)}
                    placeholder="Uses the default return URL when empty"
                  />
                </Field>
                {canManage ? (
                  <div>
                    <Button
                      variant="secondary"
                      className="min-h-11"
                      disabled={savingReturnURLs}
                      onClick={() => void saveReturnURLs()}
                    >
                      {savingReturnURLs ? "Saving…" : "Save return URLs"}
                    </Button>
                  </div>
                ) : null}
              </PanelBody>
            </Panel>
          </div>
        </TabsContent>

        <TabsContent value="providers">
          <PriorityEditor
            key={JSON.stringify(merchant.routing.providers)}
            merchantId={merchant.id}
            merchantName={merchant.name}
            environment={environment}
            routing={merchant.routing}
            providers={providers}
            onChanged={() => void load()}
            canManage={canManage}
          />
        </TabsContent>

        <TabsContent value="keys">
          <KeysPanel merchantId={merchant.id} merchantName={merchant.name} />
        </TabsContent>

        <TabsContent value="transactions">
          <MerchantTransactions
            merchantId={merchant.id}
            environment={environment}
          />
        </TabsContent>
      </TabsRoot>
    </>
  );
}

function KeysPanel({
  merchantId,
  merchantName,
}: {
  merchantId: string;
  merchantName: string;
}) {
  const { notify, auth } = useApp();
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [label, setLabel] = useState("");
  const [keyEnv, setKeyEnv] = useState<Environment>("sandbox");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [revealed, setRevealed] = useState<string | null>(null);
  const [revokeTarget, setRevokeTarget] = useState<ApiKey | null>(null);

  const canManage =
    auth.ready && auth.authenticated && auth.user.role === "owner";

  const load = useCallback(async () => {
    setLoadError(null);
    try {
      const { data } = await apiGet<RawApiKey[]>("/api-keys", {
        merchant_id: merchantId,
        limit: 100,
      });
      setKeys(data.map(mapApiKey));
      setLoaded(true);
    } catch (cause) {
      setLoadError(
        cause instanceof ApiError ? cause.message : "Cannot load API keys.",
      );
    }
  }, [merchantId]);

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(), 0);
    return () => clearTimeout(timer);
  }, [load]);

  const active = keys.filter((key) => key.status === "active").length;

  async function create() {
    if (label.trim().length < 3) {
      setError("Give the key a label of at least three characters.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const { data, meta } = await apiMutate<RawApiKey>("POST", "/api-keys", {
        merchant_id: merchantId,
        label: label.trim(),
        environment: keyEnv,
      });
      setRevealed(typeof meta.key === "string" ? meta.key : null);
      setOpen(false);
      setLabel("");
      notify(`API key created for ${merchantName} in ${keyEnv}.`);
      if (data) {
        setKeys((current) => [mapApiKey(data), ...current]);
      }
    } catch (cause) {
      setError(
        cause instanceof ApiError
          ? cause.message
          : "Could not create the key.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function revoke(key: ApiKey) {
    try {
      await apiMutate("POST", `/api-keys/${key.id}/revoke`);
      setKeys((current) =>
        current.map((entry) =>
          entry.id === key.id ? { ...entry, status: "revoked" as const } : entry,
        ),
      );
      notify(`Revoked ${key.prefix}…. Requests start failing with 401.`);
      setRevokeTarget(null);
    } catch (cause) {
      notify(
        cause instanceof ApiError
          ? `Revoke failed: ${cause.message}`
          : "Revoke failed.",
      );
    }
  }

  return (
    <Panel>
      <PanelHeader
        title="API keys"
        hint={`${active} active of ${keys.length} total. A key is shown once, then only its prefix is stored.`}
        action={
          canManage ? (
            <Button variant="primary" size="sm" onClick={() => setOpen(true)}>
              Create key
            </Button>
          ) : null
        }
      />

      {loadError ? (
        <ApiFailed
          title="API keys unavailable"
          error={loadError}
          onRetry={() => void load()}
        />
      ) : !loaded ? (
        <ApiLoading label="Loading API keys…" />
      ) : revealed ? (
        <div className="border-b border-border-subtle px-4 py-4">
          <p className="mb-2 text-[13px] text-ink">
            Copy the key now. The platform stores only its hash and prefix.
          </p>
          <CodeBlock label="api_key.txt" code={revealed} />
          <div className="mt-2">
            <Button variant="ghost" size="sm" onClick={() => setRevealed(null)}>
              I have copied it
            </Button>
          </div>
        </div>
      ) : null}

      {loaded && keys.length === 0 ? (
        <EmptyState
          title="No API keys yet"
          body="Create a key so this merchant can call the payments API. Start in sandbox, then issue a production key once testing passes."
        />
      ) : loaded && keys.length > 0 ? (
        <Table>
          <THead>
            <TR className="hover:bg-surface-muted">
              <TH>Label</TH>
              <TH>Prefix</TH>
              <TH>Environment</TH>
              <TH>Created</TH>
              <TH>Last used</TH>
              <TH>Status</TH>
              <TH className="text-right">Action</TH>
            </TR>
          </THead>
          <TBody>
            {keys.map((key) => (
              <TR key={key.id}>
                <TD>{key.label}</TD>
                <TD className="font-mono text-[12.5px]">{key.prefix}…</TD>
                <TD>
                  <Tag tone="neutral">{key.environment}</Tag>
                </TD>
                <TD className="num whitespace-nowrap text-secondary">
                  {formatDateTime(key.createdAt)}
                </TD>
                <TD className="num whitespace-nowrap text-secondary">
                  {key.lastUsedAt ? formatDateTime(key.lastUsedAt) : "never"}
                </TD>
                <TD>
                  <Tag tone={key.status === "active" ? "ok" : "neutral"}>
                    {key.status}
                  </Tag>
                </TD>
                <TD className="text-right">
                  {key.status === "active" ? (
                    canManage ? (
                      <ConfirmDialog
                        trigger={
                          <Button variant="danger" size="sm">
                            Revoke
                          </Button>
                        }
                        title="Revoke API key?"
                        description={`Revoke "${key.label}" (${key.prefix}…) for ${merchantName}? Integrations using it start failing with 401 immediately.`}
                        confirmLabel="Revoke"
                        onConfirm={() => void revoke(key)}
                      />
                    ) : (
                      <span className="text-[12px] text-muted">
                        Owner role required
                      </span>
                    )
                  ) : (
                    <span className="text-[12px] text-muted">&mdash;</span>
                  )}
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      ) : null}

      <DialogRoot
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) setError(null);
        }}
      >
        <DialogContent
          title="Create API key"
          description="The key is shown once after creation. Treat it like a password."
          footer={
            <>
              <Button variant="secondary" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                disabled={busy}
                onClick={() => void create()}
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
              error={error ?? undefined}
              help="A label helps you find the key later, for example the service that uses it."
            >
              <Input
                id="key-label"
                value={label}
                onChange={(event) => setLabel(event.target.value)}
                placeholder="Checkout production"
              />
            </Field>
            <Field label="Environment" help="Sandbox keys cannot move money.">
              <SelectControl
                ariaLabel="Key environment"
                value={keyEnv}
                onValueChange={(value) => setKeyEnv(value as Environment)}
                options={[
                  { value: "sandbox", label: "Sandbox" },
                  { value: "production", label: "Production" },
                ]}
                className="w-full"
              />
            </Field>
          </div>
        </DialogContent>
      </DialogRoot>

      <DialogRoot
        open={revokeTarget !== null}
        onOpenChange={(next) => {
          if (!next) setRevokeTarget(null);
        }}
      >
        <DialogContent
          title={`Revoke ${revokeTarget?.label ?? "key"}?`}
          description="Revoking takes effect immediately. Requests using this key start failing with 401."
          footer={
            <>
              <Button variant="secondary" onClick={() => setRevokeTarget(null)}>
                Keep key
              </Button>
              <Button
                variant="danger"
                onClick={() => {
                  if (revokeTarget) void revoke(revokeTarget);
                }}
              >
                Revoke
              </Button>
            </>
          }
        >
          <p className="text-[13px] text-secondary">
            {revokeTarget
              ? `${revokeTarget.prefix}… was created ${formatDateTime(
                  revokeTarget.createdAt,
                )} for ${revokeTarget.environment}.`
              : ""}
          </p>
        </DialogContent>
      </DialogRoot>
    </Panel>
  );
}

function MerchantTransactions({
  merchantId,
  environment,
}: {
  merchantId: string;
  environment: Environment;
}) {
  const [rows, setRows] = useState<Transaction[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    setState("loading");
    return apiGet<RawTransaction[]>("/transactions", {
      merchant_id: merchantId,
      env: environment,
      limit: 10,
    })
      .then(({ data }) => {
        setRows(data.map(mapTransaction));
        setState("ready");
      })
      .catch((cause) => {
        setState("error");
        setError(
          cause instanceof ApiError ? cause.message : "Cannot load transactions.",
        );
      });
  }, [merchantId, environment]);

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(), 0);
    return () => clearTimeout(timer);
  }, [load]);

  return (
    <Panel>
      <PanelHeader
        title="Recent transactions"
        hint={`Newest 10 in ${environment}. The full list has every filter.`}
        action={
          <Button asChild variant="secondary" size="sm">
            <Link href="/transactions">Open transactions</Link>
          </Button>
        }
      />
      {state === "loading" ? (
        <ApiLoading label="Loading transactions…" />
      ) : state === "error" ? (
        <ApiFailed
          title="Transactions unavailable"
          error={error}
          onRetry={() => void load()}
        />
      ) : rows.length === 0 ? (
        <EmptyState
          title="No transactions in this environment"
          body="This merchant has not created a payment here yet. Transactions appear once the merchant calls the payments API."
        />
      ) : (
        <Table>
          <THead>
            <TR className="hover:bg-surface-muted">
              <TH>Reference</TH>
              <TH>Provider</TH>
              <TH className="text-right">Amount</TH>
              <TH>Status</TH>
              <TH>Created</TH>
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
                <TD className="font-mono text-[12.5px]">{row.provider}</TD>
                <TD className="num text-right">
                  {new Intl.NumberFormat("id-ID").format(row.amount)}
                </TD>
                <TD>
                  <StatusTag status={row.status} />
                </TD>
                <TD className="num whitespace-nowrap text-secondary">
                  {formatDateTime(row.createdAt)}
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      )}
    </Panel>
  );
}
