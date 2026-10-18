"use client";

import { useCallback, useEffect, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { ApiFailed, ApiLoading } from "@/components/ui/api-state";
import { Button } from "@/components/ui/button";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { Field } from "@/components/ui/field";
import { SelectControl } from "@/components/ui/select";
import { Tag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ProviderConfigDialog } from "@/components/provider-config-dialog";
import type { Provider } from "@/lib/demo-data";
import {
  apiGet,
  apiMutate,
  ApiError,
  mapProvider,
  type RawProviderEntry,
} from "@/lib/api";

const stateTone = {
  active: "ok",
  configured: "accent",
  not_configured: "neutral",
} as const;

export default function ProvidersPage() {
  const { environment, notify, auth } = useApp();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [stateFilter, setStateFilter] = useState("all");
  const [target, setTarget] = useState<Provider | null>(null);
  const [removing, setRemoving] = useState<string | null>(null);

  const canManage =
    auth.ready && auth.authenticated && auth.user.role === "owner";

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const { data } = await apiGet<RawProviderEntry[]>("/providers");
      setProviders(data.map((entry) => mapProvider(entry, environment)));
    } catch (cause) {
      setError(
        cause instanceof ApiError
          ? cause.message
          : "Cannot load the provider catalog.",
      );
    } finally {
      setLoading(false);
    }
  }, [environment]);

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(), 0);
    return () => clearTimeout(timer);
  }, [load]);

  const filtered = providers.filter((provider) =>
    stateFilter === "all" ? true : provider.state === stateFilter,
  );
  const ready = providers.filter(
    (provider) => provider.state !== "not_configured",
  ).length;

  async function removeCredentials(provider: Provider) {
    setRemoving(provider.name);
    try {
      await apiMutate(
        "DELETE",
        `/providers/${provider.name}/config?env=${environment}`,
      );
      notify(
        `${provider.label} credentials removed for ${environment}. The gateway instance is retired.`,
      );
      setTarget(null);
      void load();
    } catch (cause) {
      notify(
        cause instanceof ApiError
          ? `Remove failed: ${cause.message}`
          : "Remove failed.",
      );
    } finally {
      setRemoving(null);
    }
  }

  return (
    <>
      <PageHeader
        title="Providers"
        description={`The compiled adapters and their global ${environment} credentials. A provider takes merchant traffic only once its gateway instance is live.`}
      />

      <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
        <p className="text-[13px] text-secondary">
          {ready} of {providers.length} adapters hold {environment} credentials.
          Credentials are platform-wide per environment; merchants route to the
          resulting gateway instance.
        </p>
        <Field label="Filter by state" className="w-[190px]">
          <SelectControl
            ariaLabel="Filter providers by state"
            value={stateFilter}
            onValueChange={setStateFilter}
            options={[
              { value: "all", label: "All states" },
              { value: "active", label: "Active" },
              { value: "configured", label: "Configured" },
              { value: "not_configured", label: "Needs setup" },
            ]}
            className="w-full"
          />
        </Field>
      </div>

      <Panel>
        <PanelHeader
          title="Provider catalog"
          hint={`${filtered.length} of ${providers.length} adapters`}
        />
        {error && providers.length === 0 ? (
          <ApiFailed
            title="Provider catalog unavailable"
            error={error}
            onRetry={() => void load()}
          />
        ) : loading && providers.length === 0 ? (
          <ApiLoading label="Loading provider catalog…" />
        ) : filtered.length === 0 ? (
          <EmptyState
            title="No provider in this state"
            body="Every adapter is either active, configured, or waiting for credentials. Choose a different state to see the rest."
            action={
              <Button variant="secondary" onClick={() => setStateFilter("all")}>
                Show all providers
              </Button>
            }
          />
        ) : (
          <Table>
            <THead>
              <TR className="hover:bg-surface-muted">
                <TH>Provider</TH>
                <TH>Payment methods</TH>
                <TH>State</TH>
                <TH>Gateway instance</TH>
                <TH className="text-right">Required fields</TH>
                <TH className="text-right">Action</TH>
              </TR>
            </THead>
            <TBody>
              {filtered.map((provider) => {
                const requiredCount = provider.configFields.filter(
                  (field) => field.required,
                ).length;
                return (
                  <TR key={provider.name}>
                    <TD>
                      <span className="block font-mono text-[12.5px] text-ink">
                        {provider.name}
                      </span>
                      <span className="block text-[12px] text-muted">
                        {provider.label}
                      </span>
                    </TD>
                    <TD>
                      <span className="flex max-w-[320px] flex-wrap gap-1">
                        {provider.methods.map((method) => (
                          <Tag key={method} tone="neutral">
                            {method}
                          </Tag>
                        ))}
                      </span>
                    </TD>
                    <TD>
                      <Tag tone={stateTone[provider.state]}>
                        {provider.state.replace("_", " ")}
                      </Tag>
                    </TD>
                    <TD className="font-mono text-[12px] text-secondary">
                      {provider.state === "not_configured"
                        ? "—"
                        : provider.instance}
                    </TD>
                    <TD className="num text-right">{requiredCount}</TD>
                    <TD className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant={
                            provider.state === "not_configured"
                              ? "primary"
                              : "secondary"
                          }
                          size="sm"
                          onClick={() => setTarget(provider)}
                        >
                          {provider.state === "not_configured"
                            ? "Set credentials"
                            : "Configure"}
                        </Button>
                        {provider.state !== "not_configured" && canManage ? (
                          <ConfirmDialog
                            trigger={
                              <Button
                                variant="ghost"
                                size="sm"
                                disabled={removing === provider.name}
                              >
                                Remove
                              </Button>
                            }
                            title={`Remove ${provider.label} credentials?`}
                            description={`Remove the stored ${environment} credentials for ${provider.label}? The gateway instance is retired and merchant routing to it fails over.`}
                            confirmLabel="Remove"
                            onConfirm={() => void removeCredentials(provider)}
                          />
                        ) : null}
                      </div>
                    </TD>
                  </TR>
                );
              })}
            </TBody>
          </Table>
        )}
      </Panel>

      {target ? (
        <ProviderConfigDialog
          key={`${target.name}:${target.state}`}
          provider={target}
          open={target !== null}
          onOpenChange={(open) => {
            if (!open) setTarget(null);
          }}
          environment={environment}
          configured={target.state !== "not_configured"}
          canManage={canManage}
          onSaved={(summary) => notify(summary)}
          onChanged={() => void load()}
        />
      ) : null}
    </>
  );
}
