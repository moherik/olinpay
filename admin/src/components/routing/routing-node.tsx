"use client";
import { Handle, Position, type NodeProps } from "@xyflow/react";
import { cn } from "@/lib/utils";
import { IconAlert } from "@/components/icons";
import type { RoutingNode, RoutingNodeData } from "@/lib/routing-graph";

export function RoutingNodeCard(props: NodeProps<RoutingNode>) {
  const { data, selected } = props;
  const d = data as RoutingNodeData;
  const problems = d.issues ?? [];
  const disabled = d.kind === "provider" && d.provider?.enabled === false;

  const kindLabel = kindText(d.kind);

  const title = titleText(d.kind, d);
  const subtitle = subtitleText(d.kind, d);

  return (
    <div
      className={cn(
        "w-[220px] rounded-panel border bg-surface px-3 py-2.5",
        selected ? "border-accent" : problems.length ? "border-failed" : "border-border",
        disabled && "opacity-60",
      )}
    >
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[11px] font-medium text-muted">{kindLabel}</span>
        {problems.length ? <IconAlert size={13} className="text-failed" aria-label={`${problems.length} issues`} /> : null}
      </div>
      <p className="mt-1 text-[13px] text-ink">{title}</p>
      <p className="mt-0.5 text-[12px] text-secondary">{subtitle}</p>

      {d.kind === "entry" || d.kind === "modify" ? (
        <Handle type="source" position={Position.Right} id="out" style={{ top: "50%", right: -3 }} />
      ) : null}

      {d.kind === "condition" ? (
        <>
          <Handle type="source" position={Position.Right} id="match" style={{ top: "30%", right: -3 }} />
          <Handle type="source" position={Position.Right} id="else" style={{ top: "70%", right: -3 }} />
        </>
      ) : null}

      {d.kind !== "entry" ? (
        <Handle type="target" position={Position.Left} id="in" style={{ top: "50%", left: -3 }} />
      ) : null}
    </div>
  );
}

function kindText(kind: string) {
  if (kind === "entry") return "Entry";
  if (kind === "condition") return "Condition";
  if (kind === "provider") return "Provider";
  if (kind === "modify") return "Modify";
  if (kind === "respond") return "Respond";
  return kind;
}

function titleText(kind: string, d: RoutingNodeData) {
  if (kind === "entry") return "Payment request";
  if (kind === "condition") {
    const c = d.condition;
    if (!c) return "Condition";
    const sym = c.operator === "gte" ? "≥" : c.operator === "lte" ? "≤" : c.operator === "eq" ? "=" : "≠";
    const field = c.field === "metadata" ? `metadata.${c.metadataKey || "?"}` : c.field;
    return `${field} ${sym} ${c.value || "?"}`;
  }
  if (kind === "provider") {
    return d.provider?.provider || "(not set)";
  }
  if (kind === "modify") {
    const m = d.modify;
    if (!m) return "Modify request";
    const n = m.metadata?.filter((r) => r.key && r.value).length || 0;
    return n > 0 ? `${n} metadata edit${n > 1 ? "s" : ""}` : "Modify request";
  }
  if (kind === "respond") {
    return "Defined JSON response";
  }
  return "";
}

function subtitleText(kind: string, d: RoutingNodeData) {
  if (kind === "entry") return "Starts here";
  if (kind === "provider") {
    const p = d.provider?.provider;
    if (!p) return "Select a provider";
    return d.provider?.enabled === false ? "Disabled — skipped" : "Terminal step";
  }
  if (kind === "modify") {
    const m = d.modify;
    if (m?.description) return m.description.slice(0, 60);
    const n = m?.metadata?.filter((r) => r.key && r.value).length || 0;
    return n > 0 ? `Set ${n} fields` : "Add edits";
  }
  if (kind === "respond") {
    return `status ${d.respond?.status ?? 200}`;
  }
  return "";
}