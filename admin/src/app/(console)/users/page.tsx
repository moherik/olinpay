"use client";

import { useCallback, useEffect, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { ApiFailed, ApiLoading } from "@/components/ui/api-state";
import { CodeBlock } from "@/components/ui/code";
import { IconMore } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelBody, PanelHeader } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { Tag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  DropdownContent,
  DropdownItem,
  DropdownRoot,
  DropdownSeparator,
  DropdownTrigger,
} from "@/components/ui/dropdown-menu";
import { DefList, DefRow } from "@/components/kv";
import type { AdminUser } from "@/lib/demo-data";
import {
  apiGet,
  apiMutate,
  ApiError,
  mapUser,
  type RawAdminUser,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";

const roleOptions = [
  { value: "owner", label: "Owner" },
  { value: "operator", label: "Operator" },
  { value: "tech", label: "Tech" },
];

const roleTone = {
  owner: "accent",
  operator: "neutral",
  tech: "neutral",
} as const;

export default function UsersPage() {
  const { notify, auth } = useApp();
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [inviteOpen, setInviteOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<AdminUser["role"]>("operator");
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [oneTime, setOneTime] = useState<string | null>(null);
  const [inviteEmail, setInviteEmail] = useState("");

  const [roleTarget, setRoleTarget] = useState<AdminUser | null>(null);
  const [roleValue, setRoleValue] = useState<AdminUser["role"]>("operator");

  const selfId = auth.ready && auth.authenticated ? auth.user.id : null;
  const isOwner = auth.ready && auth.authenticated && auth.user.role === "owner";

  const load = useCallback(async () => {
    setError(null);
    try {
      const { data } = await apiGet<RawAdminUser[]>("/users");
      setUsers(data.map(mapUser));
      setLoaded(true);
    } catch (cause) {
      setError(
        cause instanceof ApiError ? cause.message : "Cannot load users.",
      );
    }
  }, []);

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(), 0);
    return () => clearTimeout(timer);
  }, [load]);

  async function invite() {
    const trimmedEmail = email.trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmedEmail)) {
      setFormError("Enter an email address the person can sign in with.");
      return;
    }
    setBusy(true);
    setFormError(null);
    try {
      const { meta } = await apiMutate("POST", "/users", {
        email: trimmedEmail,
        name: name.trim(),
        role,
      });
      setOneTime(typeof meta.password === "string" ? meta.password : null);
      setInviteEmail(trimmedEmail);
      setInviteOpen(false);
      setEmail("");
      setName("");
      setRole("operator");
      notify(`Account created for ${trimmedEmail} as ${role}.`);
      void load();
    } catch (cause) {
      setFormError(
        cause instanceof ApiError
          ? cause.message
          : "Could not create the account.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function saveRole() {
    if (!roleTarget) return;
    setBusy(true);
    try {
      await apiMutate("PATCH", `/users/${roleTarget.id}`, {
        role: roleValue,
      });
      notify(`${roleTarget.email} is now ${roleValue}.`);
      setRoleTarget(null);
      void load();
    } catch (cause) {
      notify(
        cause instanceof ApiError
          ? cause.message
          : "Role change failed.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function setStatus(user: AdminUser, status: AdminUser["status"]) {
    setBusy(true);
    try {
      await apiMutate("PATCH", `/users/${user.id}`, { status });
      notify(
        `${user.email} is now ${status}. Existing sessions are ended on suspension.`,
      );
      void load();
    } catch (cause) {
      notify(
        cause instanceof ApiError ? cause.message : "Status change failed.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title="Users and roles"
        description="People who can sign in to this console. A role sets what a person can change; the backend enforces it on every request."
        actions={
          isOwner ? (
            <Button variant="primary" onClick={() => setInviteOpen(true)}>
              Invite user
            </Button>
          ) : null
        }
      />

      <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
        <Panel>
          <PanelHeader
            title="Console accounts"
            hint={`${users.filter((user) => user.status === "active").length} active of ${users.length}`}
          />
          {error && users.length === 0 ? (
            <ApiFailed
              title="Users unavailable"
              error={error}
              onRetry={() => void load()}
            />
          ) : !loaded ? (
            <ApiLoading label="Loading accounts…" />
          ) : (
            <Table>
              <THead>
                <TR className="hover:bg-surface-muted">
                  <TH>Name</TH>
                  <TH>Email</TH>
                  <TH>Role</TH>
                  <TH>Status</TH>
                  <TH>Last sign-in</TH>
                  <TH>
                    <span className="sr-only">Row actions</span>
                  </TH>
                </TR>
              </THead>
              <TBody>
                {users.map((user) => (
                  <TR key={user.id}>
                    <TD>{user.name}</TD>
                    <TD className="font-mono text-[12.5px]">{user.email}</TD>
                    <TD>
                      <Tag tone={roleTone[user.role]}>{user.role}</Tag>
                    </TD>
                    <TD>
                      <Tag tone={user.status === "active" ? "ok" : "warn"}>
                        {user.status}
                      </Tag>
                    </TD>
                    <TD className="num whitespace-nowrap text-secondary">
                      {user.lastLoginAt
                        ? formatDateTime(user.lastLoginAt)
                        : "never"}
                    </TD>
                    <TD className="text-right">
                      {isOwner && user.id !== selfId ? (
                        <DropdownRoot>
                          <DropdownTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              aria-label={`Actions for ${user.email}`}
                            >
                              <IconMore size={16} />
                            </Button>
                          </DropdownTrigger>
                          <DropdownContent>
                            <DropdownItem
                              onSelect={() => {
                                setRoleTarget(user);
                                setRoleValue(user.role);
                              }}
                            >
                              Change role
                            </DropdownItem>
                            <DropdownSeparator />
                            {user.status === "active" ? (
                              <ConfirmDialog
                                trigger={
                                  <DropdownItem
                                    tone="danger"
                                    onSelect={() => {}}
                                  >
                                    Suspend access
                                  </DropdownItem>
                                }
                                title="Suspend user access?"
                                description={`Suspend ${user.email}? They will not be able to sign in until restored, and their live sessions end immediately.`}
                                confirmLabel="Suspend"
                                onConfirm={() => void setStatus(user, "suspended")}
                              />
                            ) : (
                              <ConfirmDialog
                                trigger={
                                  <DropdownItem onSelect={() => {}}>
                                    Restore access
                                  </DropdownItem>
                                }
                                title="Restore user access?"
                                description={`Restore ${user.email}? They will be able to sign in again.`}
                                confirmLabel="Restore"
                                confirmVariant="primary"
                                onConfirm={() => void setStatus(user, "active")}
                              />
                            )}
                          </DropdownContent>
                        </DropdownRoot>
                      ) : (
                        <span className="text-[12px] text-muted">
                          {user.id === selfId ? "you" : "Owner role required"}
                        </span>
                      )}
                    </TD>
                  </TR>
                ))}
              </TBody>
            </Table>
          )}
        </Panel>

        <Panel>
          <PanelHeader title="What each role can do" />
          <PanelBody>
            <DefList>
              <DefRow label="Owner">
                Everything, including provider credentials, users, API keys and
                production keys.
              </DefRow>
              <DefRow label="Operator">
                Cancel payments, reprocess callbacks, and manage merchants and
                their callback URLs. Cannot change users, keys or credentials.
              </DefRow>
              <DefRow label="Tech">
                Read access to everything, plus webhook retries and routing
                changes. Cannot move money.
              </DefRow>
            </DefList>
            <p className="mt-3 text-[12px] text-muted">
              Every role grant and account change is written to the audit log.
            </p>
          </PanelBody>
        </Panel>
      </div>

      <DialogRoot
        open={inviteOpen}
        onOpenChange={(next) => {
          setInviteOpen(next);
          if (!next) setFormError(null);
        }}
      >
        <DialogContent
          title="Invite user"
          description="There is no email flow in this build: the console generates a one-time password for the new account."
          footer={
            <>
              <Button variant="secondary" onClick={() => setInviteOpen(false)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                disabled={busy}
                onClick={() => void invite()}
              >
                {busy ? "Creating…" : "Create account"}
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-4">
            <Field
              label="Email"
              htmlFor="invite-email"
              required
              error={formError ?? undefined}
            >
              <Input
                id="invite-email"
                type="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                placeholder="name@example.com"
                autoComplete="off"
              />
            </Field>
            <Field label="Name" htmlFor="invite-name" required>
              <Input
                id="invite-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="Admin Operator"
                autoComplete="off"
              />
            </Field>
            <Field label="Role" help="The role can be changed later by an owner.">
              <SelectControl
                ariaLabel="Invitation role"
                value={role}
                onValueChange={(value) => setRole(value as AdminUser["role"])}
                options={roleOptions}
                className="w-full"
              />
            </Field>
          </div>
        </DialogContent>
      </DialogRoot>

      <DialogRoot
        open={oneTime !== null}
        onOpenChange={(next) => {
          if (!next) setOneTime(null);
        }}
      >
        <DialogContent
          title="One-time password"
          description={`Share this password with ${inviteEmail}. It is shown once and cannot be retrieved later.`}
          footer={
            <Button variant="primary" onClick={() => setOneTime(null)}>
              Done
            </Button>
          }
        >
          <CodeBlock label="password.txt" code={oneTime ?? ""} />
        </DialogContent>
      </DialogRoot>

      <DialogRoot
        open={roleTarget !== null}
        onOpenChange={(next) => {
          if (!next) setRoleTarget(null);
        }}
      >
        <DialogContent
          title={`Change role for ${roleTarget?.email ?? "user"}`}
          description="Role changes take effect on the next request. The change is written to the audit log."
          footer={
            <>
              <Button variant="secondary" onClick={() => setRoleTarget(null)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                disabled={busy}
                onClick={() => void saveRole()}
              >
                {busy ? "Saving…" : "Save role"}
              </Button>
            </>
          }
        >
          <Field label="Role">
            <SelectControl
              ariaLabel="New role"
              value={roleValue}
              onValueChange={(value) => setRoleValue(value as AdminUser["role"])}
              options={roleOptions}
              className="w-full"
            />
          </Field>
        </DialogContent>
      </DialogRoot>
    </>
  );
}
