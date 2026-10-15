"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { Suspense, useMemo, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { DefList, DefRow, Mono } from "@/components/kv";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelBody, PanelHeader } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { Tag, StatusTag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState, ErrorState, LoadingState } from "@/components/ui/empty-state";
import { CodeBlock } from "@/components/ui/code";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import {
  TabsContent,
  TabsList,
  TabsRoot,
  TabsTrigger,
} from "@/components/ui/tabs";
import { ProviderConfigDialog } from "@/components/provider-config-dialog";
import {
  apiKeys as seedKeys,
  merchantProviders,
  providers,
  type ApiKey,
  type Environment,
} from "@/lib/demo-data";
import { formatDateTime } from "@/lib/format";

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
        <LoadingState label="Loading merchant" />
      </Panel>
    </>
  );
}

function MerchantDetailInner() {
  const params = useParams<{ id: string }>();
  const { merchants, notify, environment } = useApp();
  const merchant = merchants.find((row) => row.id === params.id);

  const [tabEnv, setTabEnv] = useState<Environment>(
    merchant?.environments[0] ?? "production",
  );
  const [configTarget, setConfigTarget] = useState<string | null>(null);

  if (!merchant) {
    return (
      <>
        <PageHeader title="Merchant not found" />
        <Panel>
          <ErrorState
            title="No merchant with that id"
            body="The id in the address does not match a merchant on this platform. It may have been mistyped, or the merchant may have been removed."
            action={
              <Button asChild variant="secondary">
                <Link href="/merchants">Back to merchants</Link>
              </Button>
            }
          />
        </Panel>
      </>
    );
  }

  const assigned = merchantProviders[merchant.id] ?? [];
  const activeProvider = providers.find(
    (provider) => provider.name === configTarget,
  );

  return (
    <>
      <PageHeader
        title={merchant.name}
        meta={
          <Tag tone={merchant.status === "active" ? "ok" : "warn"}>
            {merchant.status}
          </Tag>
        }
        description={`Merchant ${merchant.id}. Providers, routing and keys are configured per environment.`}
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
            <TabsTrigger value="providers">Provider configuration</TabsTrigger>
            <TabsTrigger value="routing">Routing</TabsTrigger>
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
                    <span className="num">{formatDateTime(merchant.createdAt)}</span>
                  </DefRow>
                  <DefRow label="Environments">
                    <span className="flex flex-wrap gap-1">
                      {merchant.environments.map((env) => (
                        <Tag key={env} tone="neutral">
                          {env}
                        </Tag>
                      ))}
                    </span>
                  </DefRow>
                  <DefRow label="API keys">{merchant.keys} active</DefRow>
                  <DefRow label="Transactions, 30d">
                    {merchant.transactions30d}
                  </DefRow>
                </DefList>
              </PanelBody>
            </Panel>

            <Panel>
              <PanelHeader
                title="Callback endpoints"
                hint="One endpoint per environment. The platform posts payment events here."
              />
              <PanelBody>
                {merchant.environments.map((env) => (
                  <DefRow key={env} label={env}>
                    <Mono>
                      {`https://${merchant.name
                        .toLowerCase()
                        .replace(/\s+/g, "-")}.example/webhooks`}
                    </Mono>
                  </DefRow>
                ))}
              </PanelBody>
            </Panel>
          </div>
        </TabsContent>

        <TabsContent value="providers">
          <div className="mb-4">
            <EnvTabs
              environments={merchant.environments}
              value={tabEnv}
              onChange={setTabEnv}
            />
          </div>
          <Panel>
            <PanelHeader
              title="Assigned providers"
              hint="A provider takes traffic for this merchant only after its credentials are saved."
            />
            {assigned.filter((row) => row.environment === tabEnv).length === 0 ? (
              <EmptyState
                title={`No providers on ${tabEnv}`}
                body="This merchant has no provider assigned in this environment. Save credentials for a provider to start routing payments to it."
                action={
                  <Button asChild variant="secondary">
                    <Link href="/providers">Open provider catalog</Link>
                  </Button>
                }
              />
            ) : (
              <Table>
                <THead>
                  <TR className="hover:bg-surface-muted">
                    <TH>Provider</TH>
                    <TH>State</TH>
                    <TH>Priority</TH>
                    <TH className="text-right">Credentials</TH>
                  </TR>
                </THead>
                <TBody>
                  {assigned
                    .filter((row) => row.environment === tabEnv)
                    .sort((a, b) => a.priority - b.priority)
                    .map((row) => (
                      <TR key={`${row.provider}-${row.environment}`}>
                        <TD className="font-mono text-[12.5px]">{row.provider}</TD>
                        <TD>
                          <Tag tone={row.state === "active" ? "ok" : "warn"}>
                            {row.state}
                          </Tag>
                        </TD>
                        <TD className="num">{row.priority}</TD>
                        <TD className="text-right">
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => setConfigTarget(row.provider)}
                          >
                            Configure
                          </Button>
                        </TD>
                      </TR>
                    ))}
                </TBody>
              </Table>
            )}
          </Panel>
        </TabsContent>

        <TabsContent value="routing">
          <div className="mb-4">
            <EnvTabs
              environments={merchant.environments}
              value={tabEnv}
              onChange={setTabEnv}
            />
          </div>
          <RoutingEditor
            key={`${merchant.id}-${tabEnv}`}
            merchantName={merchant.name}
            environment={tabEnv}
            initial={assigned
              .filter((row) => row.environment === tabEnv)
              .sort((a, b) => a.priority - b.priority)
              .map((row) => row.provider)}
            onSave={(order) =>
              notify(
                `Routing saved for ${merchant.name} / ${tabEnv}. Priority order: ${order.join(", ")}.`,
              )
            }
          />
        </TabsContent>

        <TabsContent value="keys">
          <KeysPanel merchantName={merchant.name} />
        </TabsContent>

        <TabsContent value="transactions">
          <MerchantTransactions
            merchantName={merchant.name}
            environment={environment}
          />
        </TabsContent>
      </TabsRoot>

      {activeProvider ? (
        <ProviderConfigDialog
          provider={activeProvider}
          open={configTarget !== null}
          onOpenChange={(open) => {
            if (!open) setConfigTarget(null);
          }}
          environment={tabEnv}
          scopeLabel={merchant.name}
          onSaved={(summary) => notify(summary)}
        />
      ) : null}
    </>
  );
}

function EnvTabs({
  environments,
  value,
  onChange,
}: {
  environments: Environment[];
  value: Environment;
  onChange: (value: Environment) => void;
}) {
  if (environments.length < 2) return null;
  return (
    <TabsRoot
      value={value}
      onValueChange={(next) => onChange(next as Environment)}
    >
      <TabsList>
        {environments.map((env) => (
          <TabsTrigger key={env} value={env}>
            {env}
          </TabsTrigger>
        ))}
      </TabsList>
    </TabsRoot>
  );
}

function RoutingEditor({
  merchantName,
  environment,
  initial,
  onSave,
}: {
  merchantName: string;
  environment: Environment;
  initial: string[];
  onSave: (order: string[]) => void;
}) {
  const [order, setOrder] = useState<string[]>(initial);
  const [enabled, setEnabled] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(initial.map((name) => [name, true])),
  );
  const [adding, setAdding] = useState("");

  const available = providers
    .filter((provider) => !order.includes(provider.name))
    .map((provider) => ({ value: provider.name, label: provider.label }));

  const enabledCount = order.filter((name) => enabled[name]).length;

  function move(index: number, direction: -1 | 1) {
    const target = index + direction;
    if (target < 0 || target >= order.length) return;
    const next = [...order];
    [next[index], next[target]] = [next[target], next[index]];
    setOrder(next);
  }

  return (
    <Panel>
      <PanelHeader
        title={`Routing for ${environment}`}
        hint="Payments try providers in this order. A disabled provider is skipped without failing the payment."
      />
      <PanelBody className="flex flex-col gap-2">
        <DefRow label="Strategy">
          <span>Priority order, then retry the next provider on a hard decline.</span>
        </DefRow>

        {order.length === 0 ? (
          <EmptyState
            title="No provider in the routing order"
            body="Add a provider below to give this merchant somewhere to send payments in this environment."
          />
        ) : (
          <ul className="flex flex-col gap-1.5">
            {order.map((name, index) => (
              <li
                key={name}
                className="flex flex-wrap items-center gap-2 rounded-control border border-border bg-surface px-2.5 py-2"
              >
                <span className="num w-5 text-[12px] text-muted">
                  {index + 1}
                </span>
                <span className="font-mono text-[12.5px] text-ink">{name}</span>
                <label className="ml-auto flex items-center gap-1.5 text-[12px] text-secondary">
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5 accent-accent"
                    checked={enabled[name] ?? true}
                    onChange={() =>
                      setEnabled((current) => ({
                        ...current,
                        [name]: !(current[name] ?? true),
                      }))
                    }
                  />
                  Enabled
                </label>
                <span className="flex items-center gap-1">
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => move(index, -1)}
                    disabled={index === 0}
                    aria-label={`Move ${name} up`}
                  >
                    Up
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => move(index, 1)}
                    disabled={index === order.length - 1}
                    aria-label={`Move ${name} down`}
                  >
                    Down
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      setOrder((current) =>
                        current.filter((entry) => entry !== name),
                      )
                    }
                  >
                    Remove
                  </Button>
                </span>
              </li>
            ))}
          </ul>
        )}

        <div className="flex flex-wrap items-end gap-3 pt-2">
          <Field label="Add provider" className="w-[240px]">
            <SelectControl
              ariaLabel="Add provider to routing"
              value={adding || "none"}
              onValueChange={(value) => {
                if (value === "none") return;
                setAdding("none");
                setOrder((current) => [...current, value]);
                setEnabled((current) => ({ ...current, [value]: true }));
              }}
              options={
                available.length > 0
                  ? [{ value: "none", label: "Choose a provider" }, ...available]
                  : [{ value: "none", label: "All providers already added" }]
              }
              className="w-full"
            />
          </Field>
          <Button
            variant="primary"
            disabled={order.length === 0}
            onClick={() => onSave(order)}
          >
            Save routing
          </Button>
          <span className="pb-1.5 text-[12px] text-muted">
            {enabledCount} of {order.length} providers enabled for {merchantName}.
          </span>
        </div>
      </PanelBody>
    </Panel>
  );
}

function KeysPanel({ merchantName }: { merchantName: string }) {
  const { notify } = useApp();
  const [keys, setKeys] = useState<ApiKey[]>(() =>
    seedKeys.filter((key) => key.merchant === merchantName),
  );
  const [open, setOpen] = useState(false);
  const [label, setLabel] = useState("");
  const [keyEnv, setKeyEnv] = useState<Environment>("sandbox");
  const [error, setError] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<{ label: string; secret: string } | null>(
    null,
  );
  const [revokeTarget, setRevokeTarget] = useState<ApiKey | null>(null);

  const active = keys.filter((key) => key.status === "active").length;

  function create() {
    if (label.trim().length < 3) {
      setError("Give the key a label of at least three characters.");
      return;
    }
    const random = Math.random().toString(16).slice(2, 10);
    const secret = `sk_${keyEnv === "production" ? "live" : "test"}_${random}`;
    setKeys((current) => [
      {
        id: `key_new_${random}`,
        merchant: merchantName,
        label: label.trim(),
        prefix: secret.slice(0, 12),
        environment: keyEnv,
        createdAt: new Date().toISOString(),
        lastUsedAt: null,
        status: "active",
      },
      ...current,
    ]);
    setRevealed({ label: label.trim(), secret });
    setOpen(false);
    setLabel("");
    setError(null);
    notify(`API key created for ${merchantName} in ${keyEnv}.`);
  }

  return (
    <Panel>
      <PanelHeader
        title="API keys"
        hint={`${active} active of ${keys.length} total. A key is shown once, then only its prefix is stored.`}
        action={
          <Button variant="primary" size="sm" onClick={() => setOpen(true)}>
            Create key
          </Button>
        }
      />

      {revealed ? (
        <div className="border-b border-border-subtle px-4 py-4">
          <p className="mb-2 text-[13px] text-ink">
            Copy the key now. The platform stores only its hash and prefix.
          </p>
          <CodeBlock label={revealed.label} code={revealed.secret} />
          <div className="mt-2">
            <Button variant="ghost" size="sm" onClick={() => setRevealed(null)}>
              I have copied it
            </Button>
          </div>
        </div>
      ) : null}

      {keys.length === 0 ? (
        <EmptyState
          title="No API keys yet"
          body="Create a key so this merchant can call the payments API. Start in sandbox, then issue a production key once testing passes."
          action={
            <Button variant="secondary" onClick={() => setOpen(true)}>
              Create key
            </Button>
          }
        />
      ) : (
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
                <TD className="font-mono text-[12.5px]">{key.prefix}...</TD>
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
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setRevokeTarget(key)}
                    >
                      Revoke
                    </Button>
                  ) : (
                    <span className="text-[12px] text-muted">&mdash;</span>
                  )}
                </TD>
              </TR>
            ))}
          </TBody>
        </Table>
      )}

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
              <Button variant="primary" onClick={create}>
                Create key
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
                variant="primary"
                onClick={() => {
                  if (!revokeTarget) return;
                  setKeys((current) =>
                    current.map((key) =>
                      key.id === revokeTarget.id
                        ? { ...key, status: "revoked" as const }
                        : key,
                    ),
                  );
                  notify(`Revoked ${revokeTarget.prefix}...`);
                  setRevokeTarget(null);
                }}
              >
                Revoke key
              </Button>
            </>
          }
        >
          <p className="text-[13px] text-secondary">
            {revokeTarget
              ? `${revokeTarget.prefix}... was created ${formatDateTime(
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
  merchantName,
  environment,
}: {
  merchantName: string;
  environment: Environment;
}) {
  const { transactions } = useApp();
  const rows = useMemo(
    () =>
      transactions.filter(
        (row) =>
          row.merchant === merchantName && row.environment === environment,
      ),
    [transactions, merchantName, environment],
  );

  return (
    <Panel>
      <PanelHeader
        title="Recent transactions"
        hint={`${rows.length} in ${environment}. The full list has every filter.`}
        action={
          <Button asChild variant="secondary" size="sm">
            <Link href="/transactions">Open transactions</Link>
          </Button>
        }
      />
      {rows.length === 0 ? (
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
