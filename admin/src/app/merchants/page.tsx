"use client";

import Link from "next/link";
import { useRef, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { IconMore } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { Tag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import {
  DropdownContent,
  DropdownItem,
  DropdownRoot,
  DropdownSeparator,
  DropdownTrigger,
} from "@/components/ui/dropdown-menu";
import type { Environment } from "@/lib/demo-data";
import { formatCount, formatDateTime } from "@/lib/format";

export default function MerchantsPage() {
  const { merchants, addMerchant, setMerchantStatus, notify } = useApp();
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [callbackUrl, setCallbackUrl] = useState("");
  const [environments, setEnvironments] = useState<Environment[]>(["sandbox"]);
  const [error, setError] = useState<string | null>(null);
  const counter = useRef(1);

  const needle = query.trim().toLowerCase();
  const filtered = merchants.filter((merchant) =>
    needle ? merchant.name.toLowerCase().includes(needle) : true,
  );

  function toggleEnvironment(value: Environment) {
    setEnvironments((current) =>
      current.includes(value)
        ? current.filter((item) => item !== value)
        : [...current, value],
    );
  }

  function submit() {
    const trimmed = name.trim();
    if (trimmed.length < 2) {
      setError("Enter a merchant name of at least two characters.");
      return;
    }
    if (environments.length === 0) {
      setError("Select at least one environment.");
      return;
    }
    const id = `mch_new_${counter.current++}`;
    addMerchant({
      id,
      name: trimmed,
      status: "active",
      environments,
      keys: 0,
      transactions30d: 0,
      createdAt: new Date().toISOString(),
    });
    setOpen(false);
    setName("");
    setCallbackUrl("");
    setEnvironments(["sandbox"]);
    setError(null);
    notify(
      `${trimmed} created with ${environments.join(" and ")}. Generate an API key on the merchant page to accept payments.`,
    );
  }

  return (
    <>
      <PageHeader
        title="Merchants"
        description="Each merchant holds its own providers, endpoints and keys per environment. This list is not scoped by the environment selector above."
        actions={
          <Button variant="primary" onClick={() => setOpen(true)}>
            Create merchant
          </Button>
        }
      />

      <div className="mb-4 flex items-end gap-3 rounded-panel border border-border bg-surface p-3">
        <Field label="Search merchants" htmlFor="merchant-query" className="w-[260px]">
          <Input
            id="merchant-query"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Demo Merchant"
          />
        </Field>
      </div>

      <Panel>
        <PanelHeader
          title="All merchants"
          hint={`${filtered.length} of ${merchants.length} merchants`}
        />
        {filtered.length === 0 ? (
          <EmptyState
            title="No merchant matches that name"
            body="Try a shorter search, or create the merchant if it is not on the platform yet."
            action={
              <Button variant="secondary" onClick={() => setQuery("")}>
                Clear search
              </Button>
            }
          />
        ) : (
          <Table>
            <THead>
              <TR className="hover:bg-surface-muted">
                <TH>Merchant</TH>
                <TH>Status</TH>
                <TH>Environments</TH>
                <TH className="text-right">API keys</TH>
                <TH className="text-right">Transactions, 30d</TH>
                <TH>Created</TH>
                <TH>
                  <span className="sr-only">Row actions</span>
                </TH>
              </TR>
            </THead>
            <TBody>
              {filtered.map((merchant) => (
                <TR key={merchant.id}>
                  <TD>
                    <Link
                      href={`/merchants/${merchant.id}`}
                      className="text-accent underline-offset-2 hover:underline"
                    >
                      {merchant.name}
                    </Link>
                  </TD>
                  <TD>
                    <Tag tone={merchant.status === "active" ? "ok" : "warn"}>
                      {merchant.status}
                    </Tag>
                  </TD>
                  <TD>
                    <span className="flex flex-wrap gap-1">
                      {merchant.environments.map((env) => (
                        <Tag key={env} tone="neutral">
                          {env}
                        </Tag>
                      ))}
                    </span>
                  </TD>
                  <TD className="num text-right">{merchant.keys}</TD>
                  <TD className="num text-right">
                    {formatCount(merchant.transactions30d)}
                  </TD>
                  <TD className="num whitespace-nowrap text-secondary">
                    {formatDateTime(merchant.createdAt)}
                  </TD>
                  <TD className="text-right">
                    <DropdownRoot>
                      <DropdownTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          aria-label={`Actions for ${merchant.name}`}
                        >
                          <IconMore size={16} />
                        </Button>
                      </DropdownTrigger>
                      <DropdownContent>
                        <DropdownItem
                          onSelect={() => {
                            void navigator.clipboard?.writeText(merchant.id);
                            notify(`Copied ${merchant.id} to clipboard.`);
                          }}
                        >
                          Copy merchant id
                        </DropdownItem>
                        <DropdownSeparator />
                        <DropdownItem
                          tone={
                            merchant.status === "active" ? "danger" : "default"
                          }
                          onSelect={() => {
                            const next =
                              merchant.status === "active"
                                ? "suspended"
                                : "active";
                            setMerchantStatus(merchant.id, next);
                            notify(`${merchant.name} is now ${next}.`);
                          }}
                        >
                          {merchant.status === "active"
                            ? "Suspend merchant"
                            : "Restore merchant"}
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
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) setError(null);
        }}
      >
        <DialogContent
          title="Create merchant"
          description="A merchant is a billing entity. Providers, endpoints and keys are configured per environment after creation."
          footer={
            <>
              <Button variant="secondary" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button variant="primary" onClick={submit}>
                Create merchant
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-4">
            <Field
              label="Merchant name"
              htmlFor="merchant-name"
              required
              error={error ?? undefined}
            >
              <Input
                id="merchant-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="Demo Merchant E"
              />
            </Field>
            <Field
              label="Default callback URL"
              htmlFor="merchant-callback"
              help="Where the platform posts payment events. You can set a different URL per environment."
            >
              <Input
                id="merchant-callback"
                value={callbackUrl}
                onChange={(event) => setCallbackUrl(event.target.value)}
                placeholder="https://merchant.example/webhooks"
              />
            </Field>
            <fieldset className="flex flex-col gap-2">
              <legend className="mb-1 text-[12px] font-medium text-secondary">
                Environments to enable
              </legend>
              {(["sandbox", "production"] as Environment[]).map((value) => (
                <label
                  key={value}
                  className="flex items-center gap-2 text-[13px] text-ink"
                >
                  <input
                    type="checkbox"
                    className="h-3.5 w-3.5 accent-accent"
                    checked={environments.includes(value)}
                    onChange={() => toggleEnvironment(value)}
                  />
                  {value}
                </label>
              ))}
              <p className="text-[12px] text-muted">
                Production keys are issued only after the platform owner approves
                the merchant.
              </p>
            </fieldset>
          </div>
        </DialogContent>
      </DialogRoot>
    </>
  );
}
