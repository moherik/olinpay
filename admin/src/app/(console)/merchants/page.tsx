"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { ApiFailed } from "@/components/ui/api-state";
import { IconMore } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { Tag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import { CodeBlock } from "@/components/ui/code";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  DropdownContent,
  DropdownItem,
  DropdownRoot,
  DropdownSeparator,
  DropdownTrigger,
} from "@/components/ui/dropdown-menu";
import { formatCount, formatDateTime } from "@/lib/format";

export default function MerchantsPage() {
  const {
    merchants,
    reloadMerchants,
    createMerchant,
    setMerchantStatus,
    notify,
    auth,
  } = useApp();
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [callbackUrl, setCallbackUrl] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [inviteName, setInviteName] = useState("");
  const [listError, setListError] = useState<string | null>(null);

  // Landing fresh shows the latest key counts and 30-day volumes.
  useEffect(() => {
    // Deferred a tick so context updates do not run during the effect's
    // commit.
    const timer = setTimeout(() => {
      void reloadMerchants().catch(() => {
        setListError("The merchant list could not be loaded.");
      });
    }, 0);
    return () => clearTimeout(timer);
  }, [reloadMerchants]);

  const needle = query.trim().toLowerCase();
  const filtered = merchants.filter((merchant) =>
    needle ? merchant.name.toLowerCase().includes(needle) : true,
  );
  const canManage =
    auth.ready && auth.authenticated && auth.user.role !== "tech";

  async function submit() {
    const trimmed = name.trim();
    if (trimmed.length < 2) {
      setError("Enter a merchant name of at least two characters.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const result = await createMerchant(trimmed, callbackUrl.trim());
      setOpen(false);
      setName("");
      setCallbackUrl("");
      setInviteName(trimmed);
      setSecret(result.webhookSecret ?? null);
      notify(
        `${trimmed} created. Issue an API key on the merchant page to accept payments.`,
      );
      void reloadMerchants();
    } catch (cause) {
      setError(
        cause instanceof Error
          ? cause.message
          : "Could not create the merchant.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title="Merchants"
        description="Each merchant holds its own callback URL, routing and API keys. This list is not scoped by the environment selector above."
        actions={
          canManage ? (
            <Button variant="primary" onClick={() => setOpen(true)}>
              Create merchant
            </Button>
          ) : null
        }
      />

      <div className="mb-4 flex items-end gap-3 rounded-panel border border-border bg-surface p-3">
        <Field
          label="Search merchants"
          htmlFor="merchant-query"
          className="w-[260px]"
        >
          <Input
            id="merchant-query"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Merchant name"
          />
        </Field>
      </div>

      <Panel>
        <PanelHeader
          title="All merchants"
          hint={`${filtered.length} of ${merchants.length} merchants`}
        />
        {listError && merchants.length === 0 ? (
          <ApiFailed
            title="Merchants unavailable"
            error={listError}
            onRetry={() => void reloadMerchants()}
          />
        ) : filtered.length === 0 ? (
          query.trim() ? (
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
            <EmptyState
              title="No merchants yet"
              body="Create a merchant to issue API keys, configure callbacks, and route payments. Payments cannot be created on the platform until a merchant exists."
              action={
                canManage ? (
                  <Button variant="primary" onClick={() => setOpen(true)}>
                    Create merchant
                  </Button>
                ) : undefined
              }
            />
          )
        ) : (
          <Table>
            <THead>
              <TR className="hover:bg-surface-muted">
                <TH>Merchant</TH>
                <TH>Status</TH>
                <TH>Key environments</TH>
                <TH className="text-right">Active keys</TH>
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
                      {merchant.environments.length === 0 ? (
                        <span className="text-[12px] text-muted">no keys yet</span>
                      ) : (
                        merchant.environments.map((env) => (
                          <Tag key={env} tone="neutral">
                            {env}
                          </Tag>
                        ))
                      )}
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
                        {canManage ? (
                          merchant.status === "active" ? (
                            <ConfirmDialog
                              trigger={
                                <DropdownItem tone="danger" onSelect={() => {}}>
                                  Suspend merchant
                                </DropdownItem>
                              }
                              title="Suspend merchant?"
                              description={`Suspend ${merchant.name}? Its keys stop authenticating until restored.`}
                              confirmLabel="Suspend"
                              onConfirm={() => {
                                void setMerchantStatus(merchant.id, "suspended")
                                  .then(() =>
                                    notify(`${merchant.name} is now suspended.`),
                                  )
                                  .catch(() =>
                                    notify("Status change failed."),
                                  );
                              }}
                            />
                          ) : (
                            <ConfirmDialog
                              trigger={
                                <DropdownItem onSelect={() => {}}>
                                  Restore merchant
                                </DropdownItem>
                              }
                              title="Restore merchant?"
                              description={`Restore ${merchant.name}? Its keys authenticate again.`}
                              confirmLabel="Restore"
                              confirmVariant="primary"
                              onConfirm={() => {
                                void setMerchantStatus(merchant.id, "active")
                                  .then(() =>
                                    notify(`${merchant.name} is now active.`),
                                  )
                                  .catch(() =>
                                    notify("Status change failed."),
                                  );
                              }}
                            />
                          )
                        ) : (
                          <DropdownItem disabled>
                            Manage status (needs operator role)
                          </DropdownItem>
                        )}
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
          description="A merchant is a billing entity. Its callback endpoint and keys are configured after creation; sign-in credentials for payments are issued as API keys."
          footer={
            <>
              <Button variant="secondary" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                disabled={busy}
                onClick={() => void submit()}
              >
                {busy ? "Creating…" : "Create merchant"}
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
                placeholder="Merchant name"
              />
            </Field>
            <Field
              label="Default callback URL"
              htmlFor="merchant-callback"
              help="Where the platform posts payment events."
            >
              <Input
                id="merchant-callback"
                value={callbackUrl}
                onChange={(event) => setCallbackUrl(event.target.value)}
                placeholder="https://merchant.example/webhooks"
              />
            </Field>
          </div>
        </DialogContent>
      </DialogRoot>

      <DialogRoot
        open={secret !== null}
        onOpenChange={(next) => {
          if (!next) setSecret(null);
        }}
      >
        <DialogContent
          title="Webhook signing secret"
          description={`${inviteName} has been created. This secret signs the platform's outbound callbacks to the merchant endpoint.`}
          footer={
            <Button variant="primary" onClick={() => setSecret(null)}>
              Done
            </Button>
          }
        >
          <CodeBlock label="webhook_secret.txt" code={secret ?? ""} />
          <p className="mt-2 text-[12px] text-failed">
            Shown once. Store it now — it cannot be retrieved later.
          </p>
        </DialogContent>
      </DialogRoot>
    </>
  );
}
