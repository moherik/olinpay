"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { ApiFailed, ApiLoading } from "@/components/ui/api-state";
import { EmptyState } from "@/components/ui/empty-state";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { Tag } from "@/components/ui/status-tag";
import { PriorityEditor, type RoutingEntry } from "@/components/routing/priority-editor";
import type { Environment } from "@/lib/demo-data";
import { apiGet, ApiError, type RawProviderEntry } from "@/lib/api";

type RawMerchantDetail = {
  id: string;
  name: string;
  callback_url: string;
  status: "active" | "inactive" | "suspended";
  created_at: string;
  api_key_prefix: string;
  routing: { strategy: string; providers: RoutingEntry[] };
};

/**
 * One workspace to set provider priority for every merchant. The graph
 * prototype in components/routing/routing-studio is not wired here: the
 * engine understands priority and round-robin strategies only.
 */
export default function RoutingPage() {
  const { environment, merchants, merchantsReady, auth } = useApp();
  const [picked, setPicked] = useState("");

  // The effective selection is derived: an out-of-date or empty pick falls
  // back to the first merchant, so list refreshes cannot orphan the view.
  const selected =
    merchants.find((entry) => entry.id === picked) ?? merchants[0];
  const merchantId = selected?.id ?? "";
  const merchant = merchants.find((entry) => entry.id === merchantId);

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Routing"
        description={`Provider priority per merchant, scoped to ${environment}. A payment starts at the first live provider and falls to the next on a hard decline.`}
        actions={
          merchant ? (
            <Button asChild variant="ghost">
              <Link href={`/merchants/${merchant.id}`}>Open merchant</Link>
            </Button>
          ) : null
        }
      />

      {!merchantsReady && merchants.length === 0 ? (
        <Panel>
          <ApiLoading label="Loading merchants…" />
        </Panel>
      ) : merchants.length === 0 ? (
        <Panel>
          <EmptyState
            title="No merchants to route"
            body="Create a merchant first — routing is configured per merchant. Once a merchant exists, this page sets which provider takes its payments in each environment."
            action={
              <Button asChild variant="secondary">
                <Link href="/merchants">Open merchants</Link>
              </Button>
            }
          />
        </Panel>
      ) : (
        <RoutingWorkspace
          key={`${merchantId}:${environment}`}
          merchantId={merchantId}
          merchant={merchant ?? null}
          environment={environment}
          canManage={auth.ready && auth.authenticated && auth.user.role !== "tech"}
          onMerchantChange={setPicked}
        />
      )}
    </div>
  );
}

function RoutingWorkspace({
  merchantId,
  merchant,
  environment,
  canManage,
  onMerchantChange,
}: {
  merchantId: string;
  merchant: { id: string; name: string; status: string } | null;
  environment: Environment;
  canManage: boolean;
  onMerchantChange: (value: string) => void;
}) {
  const [detail, setDetail] = useState<RawMerchantDetail | null>(null);
  const [providers, setProviders] = useState<RawProviderEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!merchantId) return;
    setLoading(true);
    setError(null);
    try {
      const { data } = await apiGet<RawMerchantDetail>(`/merchants/${merchantId}`);
      setDetail(data);
      const { data: catalog } = await apiGet<RawProviderEntry[]>("/providers");
      setProviders(catalog);
    } catch (cause) {
      setError(
        cause instanceof ApiError ? cause.message : "Cannot load routing.",
      );
    } finally {
      setLoading(false);
    }
  }, [merchantId]);

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(), 0);
    return () => clearTimeout(timer);
  }, [load]);

  if (loading && detail === null) {
    return (
      <Panel>
        <PanelHeader title="Routing" />
        <ApiLoading label={`Loading routing for ${merchant?.name ?? "merchant"}…`} />
      </Panel>
    );
  }

  if (error || detail === null) {
    return (
      <Panel>
        <PanelHeader title="Routing" />
        <ApiFailed
          title="Routing unavailable"
          error={error}
          onRetry={() => void load()}
        />
      </Panel>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end gap-3 rounded-panel border border-border bg-surface p-3">
        <Field label="Merchant" className="w-[260px]">
          <RoutingMerchantSelect
            value={merchantId}
            onChange={onMerchantChange}
          />
        </Field>
        <div className="pb-1">
          <Tag tone={detail.status === "active" ? "ok" : "warn"}>
            {detail.status}
          </Tag>
        </div>
        <Tag tone="neutral">
          {detail.routing.strategy === "round_robin"
            ? "Round-robin strategy"
            : "Priority strategy"}
        </Tag>
      </div>

      <PriorityEditor
        merchantId={detail.id}
        merchantName={detail.name}
        environment={environment}
        routing={detail.routing}
        providers={providers}
        onChanged={() => void load()}
        canManage={canManage}
      />
    </div>
  );
}

function RoutingMerchantSelect({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  const { merchants } = useApp();
  return (
    <SelectControl
      ariaLabel="Routing merchant"
      value={value}
      onValueChange={onChange}
      options={merchants.map((merchant) => ({
        value: merchant.id,
        label: merchant.name,
      }))}
      className="w-full"
    />
  );
}
