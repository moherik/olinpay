"use client";
import "@xyflow/react/dist/style.css";
import { useMemo, useState } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  Controls,
  MarkerType,
  type Connection,
  type Edge,
  type NodeChange,
  type EdgeChange,
} from "@xyflow/react";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelHeader, PanelBody } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import { CodeBlock } from "@/components/ui/code";
import { EmptyState } from "@/components/ui/empty-state";
import { ApiError, apiMutate, type RawProviderEntry } from "@/lib/api";
import type { Environment } from "@/lib/demo-data";
import {
  compileRouting,
  conditionFieldOptions,
  createNode,
  graphFromRouting,
  methodOptions,
  operatorOptions,
  opSym,
  statusOptionsRespond,
  type ConditionField,
  type ConditionOperator,
  type ModifyItem,
  type RejectPayload,
  type RoutingGraph,
  type RoutingNode,
  type RoutingNodeData,
} from "@/lib/routing-graph";
import { RoutingNodeCard } from "./routing-node";

const nodeTypes = { routing: RoutingNodeCard };

const strategyOptions = [
  { value: "priority", label: "Priority — fixed failover order" },
  { value: "round_robin", label: "Round-robin — rotate starts" },
];

const methodOptionsForCondition = [{ value: "", label: "Select method" }, ...methodOptions];

/**
 * Visual routing editor for one merchant in one environment. The graph is the
 * decision the engine executes: modify nodes right after the entry edit every
 * request before rule evaluation, conditions branch on amount, method,
 * channel, currency or request metadata (first match wins, evaluated top-down
 * along the no-match path), each match branch may edit the request further
 * through its own modify nodes, end in providers (a failover chain) or in a
 * respond node (the rule rejects the payment without a gateway call), and
 * provider nodes on the main path form the fallback chain, optionally ending
 * in a respond node for payments the whole chain declines. Saves through
 * PUT /merchants/{id}/routing; every provider node must name a live instance
 * for the environment.
 */
export function RoutingStudio({
  merchantId,
  merchantName,
  environment,
  routing,
  providers,
  canManage,
  onChanged,
  notify,
}: {
  merchantId: string;
  merchantName: string;
  environment: Environment;
  routing: {
    strategy: string;
    providers: { name: string; enabled: boolean }[];
    rules: {
      condition: { field: string; operator: string; value: string };
      providers: string[];
      apply?: ModifyItem[];
      reject?: RejectPayload;
    }[];
    enrich?: ModifyItem[];
    reject?: RejectPayload;
  };
  providers: RawProviderEntry[];
  canManage: boolean;
  onChanged: () => void;
  notify: (message: string) => void;
}) {
  const [strategy, setStrategy] = useState(routing.strategy || "priority");
  const [graph, setGraph] = useState<RoutingGraph>(() => graphFromRouting(routing));
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [compiledOpen, setCompiledOpen] = useState(false);

  // Live instances are the only gateways the engine can route to; stale
  // selections stay visible (flagged) so they can be replaced or removed.
  const liveOptions = useMemo(
    () =>
      providers
        .filter((p) => p.env_state[environment]?.active)
        .map((p) => ({
          value: p.env_state[environment].instance,
          label: `${p.label} (${p.env_state[environment].instance})`,
        })),
    [providers, environment],
  );
  const liveInstances = useMemo(() => liveOptions.map((o) => o.value), [liveOptions]);

  const compiled = useMemo(() => compileRouting(graph, strategy, liveInstances), [graph, strategy, liveInstances]);

  const providerOptions = useMemo(() => {
    const used = new Set<string>();
    graph.nodes.forEach((n) => {
      const d = n.data as RoutingNodeData;
      if (d.kind === "provider" && d.provider?.provider) used.add(d.provider.provider);
    });
    const stale = [...used]
      .filter((value) => !liveInstances.includes(value))
      .map((value) => ({ value, label: `${value} (not live)` }));
    return [{ value: "", label: "Select provider" }, ...liveOptions, ...stale];
  }, [graph, liveOptions, liveInstances]);

  const issueCountLabel = (n: number) => `${n} ${n === 1 ? "issue" : "issues"}`;
  function issueLabel(nodeId?: string) {
    if (!nodeId) return "";
    const node = graph.nodes.find((n) => n.id === nodeId);
    return node ? nodeLabelText(node) : nodeId;
  }
  function issuesFor(nodeId: string) {
    return compiled.issues.filter((i) => i.nodeId === nodeId).map((i) => i.message);
  }

  const onNodesChange = (changes: NodeChange[]) => {
    setGraph((g) => ({ ...g, nodes: applyNodeChanges(changes, g.nodes) as RoutingNode[] }));
  };
  const onEdgesChange = (changes: EdgeChange[]) => {
    setGraph((g) => ({ ...g, edges: applyEdgeChanges(changes, g.edges) as Edge[] }));
  };
  const onConnect = (connection: Connection) => {
    if (!connection.source || !connection.target) return;
    setGraph((g) => {
      let edges = g.edges.filter(
        (e) => !(e.source === connection.source && e.sourceHandle === connection.sourceHandle),
      );
      edges = addEdge(
        {
          ...connection,
          id: `e-${connection.source}-${connection.sourceHandle || "x"}-${connection.target}`,
          markerEnd: { type: MarkerType.ArrowClosed },
          label: edgeLabel(connection.sourceHandle),
        },
        edges,
      ) as Edge[];
      return { ...g, edges };
    });
  };

  const selected = graph.nodes.find((n) => n.id === selectedId) as RoutingNode | undefined;

  function updateNode(fn: (d: RoutingNodeData) => RoutingNodeData) {
    if (!selected) return;
    setGraph((g) => ({
      ...g,
      nodes: g.nodes.map((nn) => (nn.id === selected.id ? { ...nn, data: fn(nn.data as RoutingNodeData) } : nn)),
    }));
  }

  function deleteSelected() {
    if (!selected) return;
    if ((selected.data as RoutingNodeData).kind === "entry") return;
    setGraph((g) => ({
      ...g,
      nodes: g.nodes.filter((nn) => nn.id !== selected.id),
      edges: g.edges.filter((e) => e.source !== selected.id && e.target !== selected.id),
    }));
    setSelectedId(null);
  }

  function addNode(kind: "condition" | "provider" | "modify" | "respond") {
    const entry = graph.nodes.find((n) => (n.data as RoutingNodeData).kind === "entry");
    const rightmost = graph.nodes.reduce((max, n) => Math.max(max, n.position.x), 0);
    const node = createNode(kind, { x: rightmost + 240, y: entry ? entry.position.y : 160 });
    if (kind === "provider") node.data.provider = { provider: "", enabled: true };
    if (kind === "respond") node.data.respond = { status: 402, code: "", message: "" };
    setGraph((g) => ({ ...g, nodes: [...g.nodes, node] }));
    setSelectedId(node.id);
  }

  async function save() {
    if (!canManage || compiled.issues.length > 0) return;
    setSaving(true);
    const payload = {
      strategy: compiled.strategy,
      providers: compiled.providers,
      rules: compiled.rules,
      ...(compiled.enrich.length > 0 ? { enrich: compiled.enrich } : {}),
      ...(compiled.reject ? { reject: compiled.reject } : {}),
    };
    try {
      await apiMutate("PUT", `/merchants/${merchantId}/routing`, payload);
      notify(
        `Routing updated for ${merchantName}: ${compiled.rules.length} ${compiledRulesLabel(compiled.rules.length)}, ${compiled.providers.length} fallback ${compiled.providers.length === 1 ? "step" : "steps"} (${strategy}).`,
      );
      onChanged();
    } catch (cause) {
      notify(
        cause instanceof ApiError ? `Save failed: ${cause.message}` : "Save failed: request could not be sent.",
      );
    } finally {
      setSaving(false);
    }
  }

  return (
    <ReactFlowProvider>
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_320px]">
        <Panel>
          <PanelHeader
            title={`Visual routing: ${merchantName} / ${environment}`}
            hint={`${compiled.rules.length} ${compiledRulesLabel(compiled.rules.length)} • ${compiled.providers.length} fallback steps • ${compiled.issues.length === 0 ? "valid" : issueCountLabel(compiled.issues.length)}`}
            action={
              <div className="flex flex-wrap items-center gap-1">
                {!canManage ? <span className="text-[12px] text-muted">Read-only (owner role saves)</span> : null}
                {canManage ? (
                  <>
                    <Button size="sm" variant="ghost" onClick={() => setCompiledOpen(true)}>
                      View payload
                    </Button>
                    <Button
                      size="sm"
                      variant="primary"
                      disabled={saving || compiled.issues.length > 0}
                      onClick={() => void save()}
                    >
                      {saving ? "Saving…" : "Save routing"}
                    </Button>
                  </>
                ) : null}
                {compiled.issues.length > 0 ? (
                  <span className="text-[12px] text-failed">Fix {issueCountLabel(compiled.issues.length)} before saving</span>
                ) : null}
              </div>
            }
          />
          <PanelBody>
            <div className="flex flex-wrap items-end gap-3 pb-3">
              <div className="w-[300px]">
                <Field label="Strategy">
                  <SelectControl
                    ariaLabel="Routing strategy"
                    value={strategy}
                    onValueChange={setStrategy}
                    options={strategyOptions}
                    disabled={!canManage}
                    className="w-full"
                  />
                </Field>
              </div>
              <p className="pb-1.5 text-[12px] text-muted">
                Conditions are evaluated top-down — the first match routes through its match branch, then still falls
                through the fallback chain on a hard decline.
              </p>
            </div>
            <div className="routing-flow h-[420px] sm:h-[560px]">
              <ReactFlow
                nodes={graph.nodes.map((n) => ({ ...n, data: { ...(n.data as RoutingNodeData), issues: issuesFor(n.id) } }))}
                edges={graph.edges.map((e) => ({ ...e, markerEnd: { type: MarkerType.ArrowClosed } }))}
                nodeTypes={nodeTypes}
                onNodesChange={onNodesChange}
                onEdgesChange={onEdgesChange}
                onConnect={onConnect}
                onNodeClick={(_, node) => setSelectedId(node.id)}
                onPaneClick={() => setSelectedId(null)}
                snapToGrid
                snapGrid={[16, 16]}
                fitView
                fitViewOptions={{ padding: 0.2 }}
                elementsSelectable
                nodesDraggable
              >
                <Controls />
              </ReactFlow>
            </div>
            {canManage ? (
              <div className="flex flex-wrap items-center gap-1 pt-3">
                <Button size="sm" variant="secondary" onClick={() => addNode("condition")} className="gap-1.5 px-2">
                  <svg
                    width="16"
                    height="16"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.6"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    aria-hidden="true"
                  >
                    <path d="M9 18L4 12L9 6" />
                    <path d="M15 6L20 12L15 18" />
                  </svg>
                  <span>Add condition</span>
                </Button>
                <Button size="sm" variant="secondary" onClick={() => addNode("provider")} className="gap-1.5 px-2">
                  <svg
                    width="16"
                    height="16"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.6"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    aria-hidden="true"
                  >
                    <rect x="4" y="4" width="16" height="6" rx="1.5" />
                    <rect x="4" y="14" width="16" height="6" rx="1.5" />
                  </svg>
                  <span>Add provider</span>
                </Button>
                <Button size="sm" variant="secondary" onClick={() => addNode("modify")} className="gap-1.5 px-2">
                  <svg
                    width="16"
                    height="16"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.6"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    aria-hidden="true"
                  >
                    <path d="M4 9h16" />
                    <path d="M4 15h10" />
                  </svg>
                  <span>Add modify</span>
                </Button>
                <Button size="sm" variant="secondary" onClick={() => addNode("respond")} className="gap-1.5 px-2">
                  <svg
                    width="16"
                    height="16"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.6"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    aria-hidden="true"
                  >
                    <path d="M20 12a8 8 0 1 0-3 6.2" />
                    <path d="M20 17v-5h-5" />
                  </svg>
                  <span>Add respond</span>
                </Button>
              </div>
            ) : null}
          </PanelBody>
        </Panel>

        <div className="flex flex-col gap-3">
          <Panel>
            <PanelHeader title={selected ? nodeLabelText(selected) : "Config"} hint={selected ? "Edit node configuration" : "Select a node"} />
            <PanelBody className="flex flex-col gap-3">
              {!selected ? (
                <EmptyState title="No node selected" body="Click a node to edit its condition, provider, enabled state and connections." />
              ) : (
                <NodeConfig
                  selected={selected}
                  updateNode={updateNode}
                  deleteSelected={deleteSelected}
                  graph={graph}
                  setGraph={setGraph}
                  providerOptions={providerOptions}
                  canManage={canManage}
                />
              )}
            </PanelBody>
          </Panel>

          <Panel>
            <PanelHeader title="Validation" hint={issueCountLabel(compiled.issues.length)} />
            <PanelBody>
              {compiled.issues.length === 0 ? (
                <p className="text-[13px] text-muted">
                  Valid — {compiled.rules.length} conditional {compiled.rules.length === 1 ? "route" : "routes"},{" "}
                  {compiled.providers.filter((p) => p.enabled).length} of {compiled.providers.length} fallback providers
                  enabled for {environment}.
                </p>
              ) : (
                <ul className="flex flex-col gap-1 text-[13px] text-failed">
                  {compiled.issues.map((issue, idx) => (
                    <li key={idx}>
                      • {issue.message} {issue.nodeId ? `(${issueLabel(issue.nodeId)})` : ""}
                    </li>
                  ))}
                </ul>
              )}
            </PanelBody>
          </Panel>
        </div>
      </div>

      <DialogRoot open={compiledOpen} onOpenChange={setCompiledOpen}>
        <DialogContent
          title="Save payload"
          description={
            "Exactly what is sent to PUT /merchants/{id}/routing: rules are evaluated top-down (first match wins) and route through their provider chain first; the fallback chain catches declined rule routes and non-matching payments."
          }
          className="w-[min(680px,calc(100vw-32px))]"
        >
          <CodeBlock
            code={JSON.stringify(
              {
                strategy: compiled.strategy,
                providers: compiled.providers,
                rules: compiled.rules,
                ...(compiled.enrich.length > 0 ? { enrich: compiled.enrich } : {}),
                ...(compiled.reject ? { reject: compiled.reject } : {}),
              },
              null,
              2,
            )}
            label="routing.payload.json"
          />
        </DialogContent>
      </DialogRoot>
    </ReactFlowProvider>
  );
}

function compiledRulesLabel(n: number) {
  return `${n} conditional ${n === 1 ? "route" : "routes"}`;
}

function edgeLabel(sourceHandle?: string | null) {
  if (sourceHandle === "match") return "match";
  if (sourceHandle === "else") return "no match";
  return "next";
}

function NodeConfig({
  selected,
  updateNode,
  deleteSelected,
  graph,
  setGraph,
  providerOptions,
  canManage,
}: {
  selected: RoutingNode;
  updateNode: (fn: (d: RoutingNodeData) => RoutingNodeData) => void;
  deleteSelected: () => void;
  graph: RoutingGraph;
  setGraph: (fn: (g: RoutingGraph) => RoutingGraph) => void;
  providerOptions: { value: string; label: string }[];
  canManage: boolean;
}) {
  const d = selected.data as RoutingNodeData;
  const otherNodes = graph.nodes
    .filter((n) => n.id !== selected.id)
    .map((n) => ({ value: n.id, label: nodeLabelText(n) }));

  function setConn(sourceHandle: string, targetId: string | "") {
    setGraph((g) => {
      let edges = g.edges.filter((e) => !(e.source === selected.id && e.sourceHandle === sourceHandle));
      if (targetId) {
        edges = [
          ...edges,
          {
            id: `e-${selected.id}-${sourceHandle}-${targetId}`,
            source: selected.id,
            target: targetId,
            sourceHandle,
            targetHandle: "in",
            label: edgeLabel(sourceHandle),
          } as Edge,
        ];
      }
      return { ...g, edges };
    });
  }

  const getTarget = (h: string) => graph.edges.find((e) => e.source === selected.id && e.sourceHandle === h)?.target || "";

  return (
    <div className="flex flex-col gap-3">
      {d.kind === "condition" && (
        <>
          <Field label="If field">
            <SelectControl
              ariaLabel="Condition field"
              value={d.condition?.field}
              onValueChange={(v) =>
                updateNode((dd) => ({
                  ...dd,
                  condition: {
                    ...(dd.condition ?? { operator: "eq" as ConditionOperator, value: "", metadataKey: "" }),
                    field: v as ConditionField,
                    operator: v === "amount" ? "gte" : "eq",
                  },
                }))
              }
              options={conditionFieldOptions}
              disabled={!canManage}
              className="w-full"
            />
          </Field>
          {d.condition?.field === "metadata" ? (
            <Field label="Metadata key" htmlFor="routing-condition-metadata-key">
              <Input
                id="routing-condition-metadata-key"
                aria-label="Condition metadata key"
                value={d.condition?.metadataKey ?? ""}
                onChange={(e) =>
                  updateNode((dd) => ({
                    ...dd,
                    condition: {
                      ...(dd.condition ?? { field: "metadata" as ConditionField, operator: "eq" as ConditionOperator, value: "" }),
                      metadataKey: e.target.value,
                    },
                  }))
                }
                placeholder="e.g. acquirer"
                autoComplete="off"
                disabled={!canManage}
              />
            </Field>
          ) : null}
          <Field label="Comparison">
            <SelectControl
              ariaLabel="Condition operator"
              value={d.condition?.operator}
              onValueChange={(v) =>
                updateNode((dd) => ({ ...dd, condition: { ...(dd.condition ?? { field: "amount" as ConditionField, value: "", metadataKey: "" }), operator: v as ConditionOperator } }))
              }
              options={operatorOptions(d.condition?.field ?? "amount")}
              disabled={!canManage}
              className="w-full"
            />
          </Field>
          <Field label="Value" htmlFor="routing-condition-value">
            {d.condition?.field === "method" ? (
              <SelectControl
                ariaLabel="Condition method value"
                value={d.condition?.value || undefined}
                onValueChange={(v) =>
                  updateNode((dd) => ({
                    ...dd,
                    condition: {
                      ...(dd.condition ?? { field: "amount" as ConditionField, operator: "gte" as ConditionOperator, metadataKey: "" }),
                      value: v,
                    },
                  }))
                }
                options={methodOptionsForCondition}
                disabled={!canManage}
                className="w-full"
              />
            ) : (
              <Input
                id="routing-condition-value"
                aria-label="Condition value"
                value={d.condition?.value ?? ""}
                onChange={(e) =>
                  updateNode((dd) => ({
                    ...dd,
                    condition: {
                      ...(dd.condition ?? { field: "amount" as ConditionField, operator: "gte" as ConditionOperator, metadataKey: "" }),
                      value: e.target.value,
                    },
                  }))
                }
                placeholder={d.condition?.field === "amount" ? "10000" : d.condition?.field === "currency" ? "IDR" : "e.g. SHOPEE"}                autoComplete="off"
                disabled={!canManage}
              />
            )}
          </Field>
          <Field label="On match">
            <SelectControl
              ariaLabel="On match route to"
              value={getTarget("match")}
              onValueChange={(v) => setConn("match", v)}
              options={[{ value: "", label: "Not connected" }, ...otherNodes]}
              disabled={!canManage}
              className="w-full"
            />
          </Field>
          <Field label="On no match">
            <SelectControl
              ariaLabel="On no match route to"
              value={getTarget("else")}
              onValueChange={(v) => setConn("else", v)}
              options={[{ value: "", label: "End of graph" }, ...otherNodes]}
              disabled={!canManage}
              className="w-full"
            />
          </Field>
          <p className="text-[12px] text-muted">
            The match branch is tried first; whatever declines continues on the no-match path and the fallback chain.
          </p>
        </>
      )}

      {d.kind === "provider" && (
        <>
          <Field label="Provider instance">
            <SelectControl
              ariaLabel="Route to provider"
              value={d.provider?.provider ?? ""}
              onValueChange={(v) => updateNode((dd) => ({ ...dd, provider: { provider: v, enabled: dd.provider?.enabled ?? true } }))}
              options={providerOptions}
              disabled={!canManage}
              className="w-full"
            />
          </Field>
          <label className="flex items-center gap-1.5 text-[12px] text-secondary">
            <input
              type="checkbox"
              className="h-3.5 w-3.5 accent-accent"
              checked={d.provider?.enabled ?? true}
              disabled={!canManage}
              onChange={() =>
                updateNode((dd) => ({
                  ...dd,
                  provider: { ...(dd.provider ?? { provider: "" }), enabled: !(dd.provider?.enabled ?? true) },
                }))
              }
            />
            Enabled — disabled providers are skipped
          </label>
          <Field label="Sends to next">
            <SelectControl
              ariaLabel="Provider sends to"
              value={getTarget("out")}
              onValueChange={(v) => setConn("out", v)}
              options={[{ value: "", label: "End of chain (last step)" }, ...otherNodes]}
              disabled={!canManage}
              className="w-full"
            />
          </Field>
        </>
      )}

      {d.kind === "entry" && (
        <>
          <Field label="Sends to">
            <SelectControl
              ariaLabel="Entry sends to"
              value={getTarget("out")}
              onValueChange={(v) => setConn("out", v)}
              options={[{ value: "", label: "Not connected" }, ...otherNodes]}
              disabled={!canManage}
              className="w-full"
            />
          </Field>
          <p className="text-[12px] text-muted">The entry is the fixed start of every payment for this merchant.</p>
        </>
      )}

      {d.kind === "modify" && (
        <>
          <Field label="Set description" htmlFor="routing-modify-description">
            <Input
              id="routing-modify-description"
              aria-label="Modify description"
              value={d.modify?.description ?? ""}
              onChange={(e) =>
                updateNode((dd) => ({ ...dd, modify: { ...(dd.modify ?? { description: "", metadata: [] }), description: e.target.value } }))
              }
              placeholder="Empty keeps the caller's description"
              autoComplete="off"
              disabled={!canManage}
            />
          </Field>
          <p className="text-[12px] text-muted">Metadata edits</p>
          {(d.modify?.metadata ?? []).map((row, i) => (
            <div key={i} className="flex items-center gap-1.5">
              <Input
                aria-label={`Modify metadata key ${i + 1}`}
                value={row.key}
                onChange={(e) =>
                  updateNode((dd) => ({
                    ...dd,
                    modify: {
                      ...(dd.modify ?? { description: "", metadata: [] }),
                      metadata: (dd.modify?.metadata ?? []).map((r2, j) => (j === i ? { ...r2, key: e.target.value } : r2)),
                    },
                  }))
                }
                placeholder="key"
                autoComplete="off"
                disabled={!canManage}
              />
              <Input
                aria-label={`Modify metadata value ${i + 1}`}
                value={row.value}
                onChange={(e) =>
                  updateNode((dd) => ({
                    ...dd,
                    modify: {
                      ...(dd.modify ?? { description: "", metadata: [] }),
                      metadata: (dd.modify?.metadata ?? []).map((r2, j) => (j === i ? { ...r2, value: e.target.value } : r2)),
                    },
                  }))
                }
                placeholder="value"
                autoComplete="off"
                disabled={!canManage}
              />
              <Button
                size="sm"
                variant="ghost"
                aria-label={`Remove metadata row ${i + 1}`}
                onClick={() =>
                  updateNode((dd) => ({
                    ...dd,
                    modify: {
                      ...(dd.modify ?? { description: "", metadata: [] }),
                      metadata: (dd.modify?.metadata ?? [{ key: "", value: "" }]).filter((_, j) => j !== i),
                    },
                  }))
                }
                disabled={!canManage}
              >
                ×
              </Button>
            </div>
          ))}
          <Button
            size="sm"
            variant="ghost"
            aria-label="Add metadata row"
            onClick={() =>
              updateNode((dd) => ({
                ...dd,
                modify: { ...(dd.modify ?? { description: "", metadata: [] }), metadata: [...(dd.modify?.metadata ?? []), { key: "", value: "" }] },
              }))
            }
            disabled={!canManage}
          >
            + Add metadata
          </Button>
          <Field label="Sends to">
            <SelectControl
              ariaLabel="Modify sends to"
              value={getTarget("out")}
              onValueChange={(v) => setConn("out", v)}
              options={[{ value: "", label: "End of graph" }, ...otherNodes]}
              disabled={!canManage}
              className="w-full"
            />
          </Field>
          <p className="text-[12px] text-muted">
            Directly after the entry, edits run for every request before conditions evaluate; inside a match branch, edits run for that rule
            only, before its providers.
          </p>
        </>
      )}

      {d.kind === "respond" && (
        <>
          <Field label="HTTP status">
            <SelectControl
              ariaLabel="Respond status"
              value={String(d.respond?.status ?? 402)}
              onValueChange={(v) => updateNode((dd) => ({ ...dd, respond: { ...(dd.respond ?? { code: "", message: "" }), status: Number(v) } }))}
              options={statusOptionsRespond}
              disabled={!canManage}
              className="w-full"
            />
          </Field>
          <Field label="Error code" htmlFor="routing-respond-code">
            <Input
              id="routing-respond-code"
              aria-label="Respond code"
              value={d.respond?.code ?? ""}
              onChange={(e) =>
                updateNode((dd) => ({ ...dd, respond: { ...(dd.respond ?? { status: 402, message: "" }), code: e.target.value } }))
              }
              placeholder="e.g. country_not_supported"
              autoComplete="off"
              disabled={!canManage}
            />
          </Field>
          <Field label="Message" htmlFor="routing-respond-message">
            <Input
              id="routing-respond-message"
              aria-label="Respond message"
              value={d.respond?.message ?? ""}
              onChange={(e) =>
                updateNode((dd) => ({ ...dd, respond: { ...(dd.respond ?? { status: 402, code: "" }), message: e.target.value } }))
              }
              placeholder="e.g. Payments from this country are not accepted"
              autoComplete="off"
              disabled={!canManage}
            />
          </Field>
          <p className="text-[12px] text-muted">
            This node never calls a gateway. On the payment API it answers the standard error shape: {"{ error: { code, message } }"} with the
            status above. Inside a match branch it rejects that rule alone; past the fallback chain it answers when every provider declines.
          </p>
        </>
      )}

      {d.kind !== "entry" && (
        <div className="pt-2">
          <Button size="sm" variant="danger" onClick={deleteSelected} disabled={!canManage}>
            Delete node
          </Button>
        </div>
      )}
    </div>
  );
}

function nodeLabelText(n: RoutingNode) {
  const d = n.data as RoutingNodeData;
  if (d.kind === "entry") return "Entry";
  if (d.kind === "condition") {
    const c = d.condition;
    if (!c) return "Condition";
    const field = c.field === "metadata" ? `metadata.${c.metadataKey || "?"}` : c.field;
    return `${field} ${opSym(c.operator)} ${c.value || "?"}`;
  }
  if (d.kind === "provider") return d.provider?.provider || "Provider";
  if (d.kind === "respond") {
    const r = d.respond;
    return r ? `Respond ${r.status} ${r.code || ""}`.trim() : "Respond";
  }
  return d.label;
}
