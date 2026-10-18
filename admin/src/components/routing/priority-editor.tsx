"use client";

import Link from "next/link";
import { useCallback, useState } from "react";
import { useApp } from "@/components/shell/app-context";
import { DefRow } from "@/components/kv";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Panel, PanelBody, PanelHeader } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { EmptyState } from "@/components/ui/empty-state";
import type { Environment } from "@/lib/demo-data";
import { apiMutate, ApiError, type RawProviderEntry } from "@/lib/api";

/** Routing entries as stored by the engine. */
export type RoutingEntry = { name: string; enabled: boolean; priority: number };

/**
 * Priority-order editor for one merchant in one environment. Sandbox provider
 * instances are suffixed ".sandbox"; everything else aims at production.
 * Only live gateway instances are offered — the engine rejects anything else.
 */
export function PriorityEditor({
  merchantId,
  merchantName,
  environment,
  routing,
  providers,
  onChanged,
  canManage,
}: {
  merchantId: string;
  merchantName: string;
  environment: Environment;
  routing: { strategy: string; providers: RoutingEntry[] };
  providers: RawProviderEntry[];
  onChanged: () => void;
  canManage: boolean;
}) {
  const { notify } = useApp();

  const scoped = useCallback(
    (list: RoutingEntry[]) =>
      (list ?? []).filter((entry) => {
        const isSandbox = entry.name.endsWith(".sandbox");
        return environment === "sandbox" ? isSandbox : !isSandbox;
      }),
    [environment],
  );

  // The parent keys this component by routing content, so a fresh routing
  // config arrives as a fresh mount; the order below is the initial state.
  const [order, setOrder] = useState<RoutingEntry[]>(() =>
    scoped(routing.providers).sort((a, b) => a.priority - b.priority),
  );
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);

  // Only live instances are routable — the engine rejects anything else.
  const available = providers
    .filter((provider) => provider.env_state[environment]?.active)
    .filter(
      (provider) =>
        !order.some(
          (entry) => entry.name === provider.env_state[environment].instance,
        ),
    )
    .map((provider) => ({
      value: provider.env_state[environment].instance,
      label: `${provider.label} (${provider.env_state[environment].instance})`,
    }));

  const enabledCount = order.filter((entry) => entry.enabled).length;

  function move(index: number, direction: -1 | 1) {
    const target = index + direction;
    if (target < 0 || target >= order.length) return;
    const next = [...order];
    [next[index], next[target]] = [next[target], next[index]];
    setOrder(next);
    setDirty(true);
  }

  async function save() {
    if (!dirty) return;
    setSaving(true);
    try {
      // The stored config is shared by both environments: keep the entries
      // of the other environment untouched, replace only this environment's
      // slice. Instance suffixes carry the environment.
      const other = (routing.providers ?? [])
        .filter((entry) => {
          const isSandbox = entry.name.endsWith(".sandbox");
          return environment === "sandbox" ? !isSandbox : isSandbox;
        })
        .map((entry) => ({
          name: entry.name,
          enabled: entry.enabled,
          priority: 0,
        }));
      await apiMutate("PUT", `/merchants/${merchantId}/routing`, {
        strategy: routing.strategy || "priority",
        providers: [...other, ...order.map((entry) => ({ ...entry }))],
      });
      notify(`Routing saved for ${merchantName} in ${environment}.`);
      setDirty(false);
      onChanged();
    } catch (cause) {
      notify(
        cause instanceof ApiError
          ? `Routing save failed: ${cause.message}`
          : "Routing save failed.",
      );
    } finally {
      setSaving(false);
    }
  }

  return (
    <Panel>
      <PanelHeader
        title={`Routing for ${environment}`}
        hint="Payments try providers in this order. A disabled provider is skipped without failing the payment. Only live providers can be added."
      />
      <PanelBody className="flex flex-col gap-2">
        <DefRow label="Strategy">
          <span>Priority order, then retry the next provider on a hard decline.</span>
        </DefRow>

        {order.length === 0 ? (
          <EmptyState
            title="No provider in the routing order"
            body={
              available.length === 0
                ? "No provider is live in this environment yet. Save credentials on the providers page first — a provider without credentials cannot take traffic."
                : "Add a provider below to give this merchant somewhere to send payments in this environment."
            }
            action={
              available.length === 0 ? (
                <Button asChild variant="secondary" size="sm">
                  <Link href="/providers">Open providers</Link>
                </Button>
              ) : undefined
            }
          />
        ) : (
          <ul className="flex flex-col gap-1.5">
            {order.map((entry, index) => (
              <li
                key={entry.name}
                className="flex flex-wrap items-center gap-2 rounded-control border border-border bg-surface px-2.5 py-2"
              >
                <span className="num w-5 text-[12px] text-muted">
                  {index + 1}
                </span>
                <span className="font-mono text-[12.5px] text-ink">
                  {entry.name}
                </span>
                <label className="ml-auto flex items-center gap-1.5 text-[12px] text-secondary">
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5 accent-accent"
                    checked={entry.enabled}
                    disabled={!canManage}
                    onChange={() => {
                      setOrder((current) =>
                        current.map((item, itemIndex) =>
                          itemIndex === index
                            ? { ...item, enabled: !item.enabled }
                            : item,
                        ),
                      );
                      setDirty(true);
                    }}
                  />
                  Enabled
                </label>
                <span className="flex items-center gap-1">
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={!canManage || index === 0}
                    onClick={() => move(index, -1)}
                    aria-label={`Move ${entry.name} up`}
                  >
                    Up
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={!canManage || index === order.length - 1}
                    onClick={() => move(index, 1)}
                    aria-label={`Move ${entry.name} down`}
                  >
                    Down
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={!canManage}
                    onClick={() => {
                      setOrder((current) =>
                        current.filter((item) => item.name !== entry.name),
                      );
                      setDirty(true);
                    }}
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
              value="none"
              onValueChange={(value) => {
                if (value === "none") return;
                setOrder((current) => [
                  ...current,
                  { name: value, enabled: true, priority: current.length },
                ]);
                setDirty(true);
              }}
              options={
                available.length > 0
                  ? [{ value: "none", label: "Choose a provider" }, ...available]
                  : [{ value: "none", label: "No live provider to add" }]
              }
              className="w-full"
            />
          </Field>
          <Button
            variant="primary"
            disabled={!canManage || !dirty || saving}
            onClick={() => void save()}
          >
            {saving ? "Saving…" : "Save routing"}
          </Button>
          <span className="pb-1.5 text-[12px] text-muted">
            {enabledCount} of {order.length} providers enabled for {environment}.
          </span>
        </div>
      </PanelBody>
    </Panel>
  );
}
