"use client";

import { useCallback, useEffect, useState } from "react";
import { PageHeader } from "@/components/shell/page-header";
import { ApiFailed, ApiLoading } from "@/components/ui/api-state";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelHeader } from "@/components/ui/panel";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { EmptyState } from "@/components/ui/empty-state";
import { Button } from "@/components/ui/button";
import {
  apiGet,
  ApiError,
  type RawAuditEntry,
} from "@/lib/api";
import { formatDateTime } from "@/lib/format";

const PAGE_SIZE = 50;

export default function AuditPage() {
  const [rows, setRows] = useState<RawAuditEntry[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [queryInput, setQueryInput] = useState("");
  const [query, setQuery] = useState("");

  // Search text is debounced so typing does not fire one request per
  // keystroke.
  useEffect(() => {
    const timer = setTimeout(() => setQuery(queryInput), 300);
    return () => clearTimeout(timer);
  }, [queryInput]);

  const load = useCallback(
    async (offset: number, append: boolean) => {
      setLoading(true);
      setError(null);
      try {
        const { data, meta } = await apiGet<RawAuditEntry[]>("/audit", {
          q: query,
          limit: PAGE_SIZE,
          offset,
        });
        setRows((current) => (append ? [...current, ...data] : data));
        setHasMore(meta.has_more === true);
      } catch (cause) {
        setError(
          cause instanceof ApiError ? cause.message : "Cannot load the audit log.",
        );
      } finally {
        setLoading(false);
      }
    },
    [query],
  );

  useEffect(() => {
    // Deferred a tick so the loader's state updates do not run during the
    // effect's commit.
    const timer = setTimeout(() => void load(0, false), 0);
    return () => clearTimeout(timer);
  }, [load]);

  const needle = query.trim();

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
            value={queryInput}
            onChange={(event) => setQueryInput(event.target.value)}
            placeholder="provider_credentials.updated"
          />
        </Field>
        {needle ? (
          <Button
            variant="ghost"
            onClick={() => {
              setQueryInput("");
            }}
          >
            Clear search
          </Button>
        ) : null}
      </div>

      <Panel>
        <PanelHeader
          title="Recent entries"
          hint={`Showing the newest ${rows.length} matching entries`}
        />
        {error && rows.length === 0 ? (
          <ApiFailed
            title="Audit log unavailable"
            error={error}
            onRetry={() => void load(0, false)}
          />
        ) : loading && rows.length === 0 ? (
          <ApiLoading label="Loading audit entries…" />
        ) : rows.length === 0 ? (
          <EmptyState
            title={needle ? "No entry matches that search" : "No audit entries yet"}
            body={
              needle
                ? "Search runs over the actor, the action, and the target. Try a shorter term such as the action name or an email address."
                : "Actions in this console are recorded here as they happen. Make a change — cancelling a payment, saving credentials — and it appears in this log."
            }
            action={
              needle ? (
                <Button variant="secondary" onClick={() => setQueryInput("")}>
                  Clear search
                </Button>
              ) : undefined
            }
          />
        ) : (
          <>
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
                    <TD>{row.target ?? "—"}</TD>
                    <TD className="font-mono text-[12.5px] text-secondary">
                      {row.ip ?? "—"}
                    </TD>
                  </TR>
                ))}
              </TBody>
            </Table>
            <div className="flex justify-center border-t border-border-subtle p-3">
              {hasMore ? (
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={loading}
                  onClick={() => void load(rows.length, true)}
                >
                  {loading ? "Loading…" : "Load more"}
                </Button>
              ) : (
                <span className="text-[12px] text-muted">
                  End of log — {rows.length} entries
                </span>
              )}
            </div>
          </>
        )}
      </Panel>
    </>
  );
}
