"use client";

import { useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { Button } from "@/components/ui/button";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { Field } from "@/components/ui/field";
import { SelectControl } from "@/components/ui/select";
import { Tag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import { ProviderConfigDialog } from "@/components/provider-config-dialog";
import { providers, type Provider } from "@/lib/demo-data";

const stateTone = {
  active: "ok",
  configured: "accent",
  not_configured: "neutral",
} as const;

export default function ProvidersPage() {
  const { environment, notify } = useApp();
  const [stateFilter, setStateFilter] = useState("all");
  const [target, setTarget] = useState<Provider | null>(null);

  const filtered = providers.filter((provider) =>
    stateFilter === "all" ? true : provider.state === stateFilter,
  );
  const ready = providers.filter((provider) => provider.state !== "not_configured").length;

  return (
    <>
      <PageHeader
        title="Providers"
        description="The platform ships these adapters. A provider starts taking traffic for a merchant only once its credentials are saved. Each provider asks for a different set of fields."
      />

      <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
        <p className="text-[13px] text-secondary">
          {ready} of {providers.length} providers have credentials. A platform
          default applies everywhere until a merchant overrides it.
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
        {filtered.length === 0 ? (
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
                <TH>Environments</TH>
                <TH>State</TH>
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
                      <span className="flex max-w-[360px] flex-wrap gap-1">
                        {provider.methods.map((method) => (
                          <Tag key={method} tone="neutral">
                            {method}
                          </Tag>
                        ))}
                      </span>
                    </TD>
                    <TD>
                      <span className="flex flex-wrap gap-1">
                        {provider.sandbox ? (
                          <Tag tone="neutral">sandbox</Tag>
                        ) : null}
                        {provider.production ? (
                          <Tag tone="neutral">production</Tag>
                        ) : null}
                      </span>
                    </TD>
                    <TD>
                      <Tag tone={stateTone[provider.state]}>
                        {provider.state.replace("_", " ")}
                      </Tag>
                    </TD>
                    <TD className="num text-right">{requiredCount}</TD>
                    <TD className="text-right">
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
          provider={target}
          open={target !== null}
          onOpenChange={(open) => {
            if (!open) setTarget(null);
          }}
          environment={environment}
          scopeLabel="Platform"
          allowMerchantScope
          allowEnvironmentSelect
          onSaved={(summary) => notify(summary)}
        />
      ) : null}
    </>
  );
}
