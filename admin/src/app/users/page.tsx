"use client";

import { useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { IconMore } from "@/components/icons";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelBody, PanelHeader } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { Tag } from "@/components/ui/status-tag";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import {
  DropdownContent,
  DropdownItem,
  DropdownRoot,
  DropdownSeparator,
  DropdownTrigger,
} from "@/components/ui/dropdown-menu";
import { DefList, DefRow } from "@/components/kv";
import { adminUsers, type AdminUser } from "@/lib/demo-data";
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
  const { notify } = useApp();
  const [users, setUsers] = useState<AdminUser[]>(adminUsers);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<AdminUser["role"]>("operator");
  const [error, setError] = useState<string | null>(null);
  const [roleTarget, setRoleTarget] = useState<AdminUser | null>(null);
  const [roleValue, setRoleValue] = useState<AdminUser["role"]>("operator");

  function invite() {
    const trimmedEmail = email.trim();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(trimmedEmail)) {
      setError("Enter an email address the person can receive mail at.");
      return;
    }
    if (name.trim().length < 2) {
      setError("Enter a name of at least two characters.");
      return;
    }
    if (users.some((user) => user.email === trimmedEmail)) {
      setError("That email already has an account.");
      return;
    }
    setUsers((current) => [
      {
        id: `usr_new_${current.length + 1}`,
        email: trimmedEmail,
        name: name.trim(),
        role,
        status: "active",
        lastLoginAt: "",
      },
      ...current,
    ]);
    notify(`Invitation sent to ${trimmedEmail} as ${role}.`);
    setInviteOpen(false);
    setEmail("");
    setName("");
    setRole("operator");
    setError(null);
  }

  return (
    <>
      <PageHeader
        title="Users and roles"
        description="People who can sign in to this console. A role sets what a person can change. This demo build lists the roles but does not enforce them."
        actions={
          <Button variant="primary" onClick={() => setInviteOpen(true)}>
            Invite user
          </Button>
        }
      />

      <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
        <Panel>
          <PanelHeader
            title="Console accounts"
            hint={`${users.filter((user) => user.status === "active").length} active of ${users.length}`}
          />
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
                    {user.lastLoginAt ? formatDateTime(user.lastLoginAt) : "never"}
                  </TD>
                  <TD className="text-right">
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
                        <DropdownItem
                          tone={user.status === "active" ? "danger" : "default"}
                          onSelect={() => {
                            const nextStatus =
                              user.status === "active" ? "suspended" : "active";
                            setUsers((current) =>
                              current.map((row) =>
                                row.id === user.id
                                  ? { ...row, status: nextStatus }
                                  : row,
                              ),
                            );
                            notify(
                              `${user.email} is now ${nextStatus}. Existing sessions are ended.`,
                            );
                          }}
                        >
                          {user.status === "active" ? "Suspend access" : "Restore access"}
                        </DropdownItem>
                      </DropdownContent>
                    </DropdownRoot>
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        </Panel>

        <Panel>
          <PanelHeader title="What each role can do" />
          <PanelBody>
            <DefList>
              <DefRow label="Owner">
                Everything, including provider credentials, users, and
                production keys.
              </DefRow>
              <DefRow label="Operator">
                Transactions, refunds, webhook retries, and merchant settings.
                Cannot change users or credentials.
              </DefRow>
              <DefRow label="Tech">
                Read access to everything, plus webhook retries and routing.
                Cannot move money.
              </DefRow>
            </DefList>
          </PanelBody>
        </Panel>
      </div>

      <DialogRoot
        open={inviteOpen}
        onOpenChange={(next) => {
          setInviteOpen(next);
          if (!next) setError(null);
        }}
      >
        <DialogContent
          title="Invite user"
          description="The person receives a link to set a password. They can sign in as soon as they do."
          footer={
            <>
              <Button variant="secondary" onClick={() => setInviteOpen(false)}>
                Cancel
              </Button>
              <Button variant="primary" onClick={invite}>
                Send invitation
              </Button>
            </>
          }
        >
          <div className="flex flex-col gap-4">
            <Field
              label="Email"
              htmlFor="invite-email"
              required
              error={error ?? undefined}
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
                placeholder="Admin Operator 3 (demo)"
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
                onClick={() => {
                  if (!roleTarget) return;
                  setUsers((current) =>
                    current.map((row) =>
                      row.id === roleTarget.id ? { ...row, role: roleValue } : row,
                    ),
                  );
                  notify(`${roleTarget.email} is now ${roleValue}.`);
                  setRoleTarget(null);
                }}
              >
                Save role
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
