"use client";

import { useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import { Button } from "@/components/ui/button";
import { auditLog } from "@/lib/demo-data";
import { formatDateTime } from "@/lib/format";

export default function AuditPage() {
  const [query, setQuery] = useState("");

  const needle = query.trim().toLowerCase();
  const rows = auditLog.filter((row) =>
    needle
      ? row.actor.toLowerCase().includes(needle) ||
        row.action.toLowerCase().includes(needle) ||
        row.target.toLowerCase().includes(needle)
      : true,
  );

  return (
    <>
      <PageHeader
        title="Audit log"
        description="Every change made in this console, who made it, and when. The log is append-only. Entries cannot be edited or removed from the console."
      />

      <div className="mb-4 flex items-end gap-3 rounded-panel border border-border bg-surface p-3">
        <Field
          label="Search actor, action or target"
          htmlFor="audit-query"
          className="w-[320px]"
        >
          <Input
            id="audit-query"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="provider_credentials.updated"
          />
        </Field>
      </div>

      <Panel>
        <PanelHeader
          title="Recent entries"
          hint={`${rows.length} of ${auditLog.length} entries`}
        />
        {rows.length === 0 ? (
          <EmptyState
            title="No entry matches that search"
            body="Search runs over the actor, the action, and the target. Try a shorter term such as the action name or an email address."
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
                <TH>When</TH>
                <TH>Actor</TH>
                <TH>Action</TH>
                <TH>Target</TH>
                <TH>Source address</TH>
              </TR>
            </THead>
            <TBody>
              {rows.map((row) => (
                <TR key={row.id}>
                  <TD className="num whitespace-nowrap text-secondary">
                    {formatDateTime(row.at)}
                  </TD>
                  <TD className="font-mono text-[12.5px]">{row.actor}</TD>
                  <TD className="font-mono text-[12.5px]">{row.action}</TD>
                  <TD>{row.target}</TD>
                  <TD className="font-mono text-[12.5px] text-secondary">
                    {row.ip}
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Panel>
    </>
  );
}
