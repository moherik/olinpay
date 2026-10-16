"use client";
import "@xyflow/react/dist/style.css";
import { useMemo, useRef, useState } from "react";
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
import { Textarea } from "@/components/ui/textarea";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import { CodeBlock } from "@/components/ui/code";
import { EmptyState } from "@/components/ui/empty-state";
import { providers as demoProviders } from "@/lib/demo-data";
import type { Environment } from "@/lib/demo-data";
import {
  conditionFieldOptions,
  envOptions,
  initialGraph,
  methodOptions,
  operatorOptions,
  statusOptionsRespond,
  type ConditionOperator,
  type RoutingNode,
  type RoutingNodeData,
  type RoutingGraph,
  validateGraph,
  compileToRoutingConfig,
  isValidJSON,
  createNode,
  opSym,
  type ConditionField,
} from "@/lib/routing-graph";
import { RoutingNodeCard } from "./routing-node";

const nodeTypes = { routing: RoutingNodeCard };

function getGraphKey(m: string, e: Environment) {
  return `${m}:${e}`;
}

export function RoutingStudio({
  merchants,
  environment,
  notify,
}: {
  merchants: { id: string; name: string; environments: Environment[] }[];
  environment: Environment;
  notify: (msg: string) => void;
}) {
  const [graphs, setGraphs] = useState<Record<string, RoutingGraph>>(() => {
    const g: Record<string, RoutingGraph> = {};
    g[getGraphKey("mch_demo_a", "production")] = initialGraph();
    return g;
  });
  const [merchantId, setMerchantId] = useState(merchants[0]?.id ?? "mch_demo_a");
  const merchant = merchants.find((m) => m.id === merchantId) || merchants[0];

  const scope = getGraphKey(merchant?.id ?? merchantId, environment);
  const graph = useMemo(() => graphs[scope] ?? { nodes: [], edges: [] }, [graphs, scope]);

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [compiledOpen, setCompiledOpen] = useState(false);
  const issues = useMemo(() => validateGraph(graph.nodes, graph.edges), [graph]);
  const issueCountLabel = (n: number) => `${n} ${n === 1 ? "issue" : "issues"}`;
  function issueLabel(nodeId?: string) {
    if (!nodeId) return "";
    const node = graph.nodes.find((n) => n.id === nodeId);
    return node ? nodeLabelText(node) : nodeId;
  }

  function issuesFor(nodeId: string) {
    return issues.filter((i) => i.nodeId === nodeId).map((i) => i.message);
  }

  function setCurrent(fn: (g: RoutingGraph) => RoutingGraph) {
    setGraphs((gs) => ({ ...gs, [scope]: fn(gs[scope] ?? { nodes: [], edges: [] }) }));
  }

  const onNodesChange = (changes: NodeChange[]) => {
    setCurrent((g) => ({ ...g, nodes: applyNodeChanges(changes, g.nodes) as RoutingNode[] }));
  };
  const onEdgesChange = (changes: EdgeChange[]) => {
    setCurrent((g) => ({ ...g, edges: applyEdgeChanges(changes, g.edges) as Edge[] }));
  };
  const onConnect = (connection: Connection) => {
    if (!connection.source || !connection.target) return;
    setCurrent((g) => {
      let edges = g.edges.filter((e) => !(e.source === connection.source && e.sourceHandle === connection.sourceHandle));
      edges = addEdge(
        {
          ...connection,
          id: `e-${connection.source}-${connection.sourceHandle || "x"}-${connection.target}`,
          markerEnd: { type: MarkerType.ArrowClosed },
          label: labelForHandle(connection.sourceHandle),
        },
        edges,
      ) as Edge[];
      return { ...g, edges };
    });
  };
  const isValidConnection = (conn: Connection | Edge) => {
    if (!conn.source || !conn.target) return false;
    if (conn.source === conn.target) return false;
    return true;
  };

  const flowRef = useRef<HTMLDivElement>(null);
  const addNode = (kind: "condition" | "provider" | "modify" | "respond") => {
    const rect = flowRef.current?.getBoundingClientRect();
    let x = 280,
      y = 160;
    if (rect) {
      x = rect.left + rect.width / 2 - 110;
      y = rect.top + rect.height / 2 - 60;
    }
    const n = createNode(kind, { x, y });
    setCurrent((g) => ({ ...g, nodes: [...g.nodes, n] }));
    setSelectedId(n.id);
  };

  const selected = graph.nodes.find((n) => n.id === selectedId) as RoutingNode | undefined;

  function updateNode(fn: (d: RoutingNodeData) => RoutingNodeData) {
    if (!selected) return;
    setCurrent((g) => ({ ...g, nodes: g.nodes.map((nn) => (nn.id === selected.id ? { ...nn, data: fn(nn.data as RoutingNodeData) } : nn)) }));
  }

  function deleteSelected() {
    if (!selected) return;
    if (selected.data.kind === "entry") return;
    setCurrent((g) => ({ ...g, nodes: g.nodes.filter((nn) => nn.id !== selected.id), edges: g.edges.filter((e) => e.source !== selected.id && e.target !== selected.id) }));
    setSelectedId(null);
  }

  const compiled = compileToRoutingConfig(graph.nodes, graph.edges);
  const merchantOptions = merchants
    .filter((m) => m.environments.includes(environment))
    .map((m) => ({ value: m.id, label: m.name }));
  const providerOptions = [{ value: "", label: "Select provider" }, ...demoProviders.map((p) => ({ value: p.name, label: `${p.label} (${p.name})` }))];

  return (
    <ReactFlowProvider>
      <div className="flex flex-col gap-3">
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_320px]">
          <Panel>
            <PanelHeader
              title={`Graph: ${merchant?.name ?? merchantId} / ${environment}`}
              hint={`${graph.nodes.length} nodes, ${graph.edges.length} edges • ${issueCountLabel(issues.length)}`}
              action={
                <div className="flex flex-wrap items-center gap-1">

                  <Button size="sm" variant="ghost" onClick={() => setCompiledOpen(true)}>
                    View compiled
                  </Button>
                  <Button
                    size="sm"
                    variant="primary"
                    disabled={issues.length > 0}
                    onClick={() => notify("Routing graph saved (demo build, changes stay in this browser session).")}
                  >
                    Save routing
                  </Button>
                  {issues.length > 0 ? (
                    <span className="text-[12px] text-failed">Fix {issueCountLabel(issues.length)} before saving</span>
                  ) : null}
                </div>
              }
            />
            <PanelBody>
              <div className="flex flex-wrap items-center gap-2 pb-3">
                <div className="w-56">
                  <Field label="Merchant">
                    <SelectControl ariaLabel="Routing merchant" value={merchantId} onValueChange={setMerchantId} options={merchantOptions} className="w-full" />
                  </Field>
                </div>
              </div>
              <div ref={flowRef} className="routing-flow h-[420px] sm:h-[560px]">
                <ReactFlow
                  nodes={graph.nodes.map((n) => ({ ...n, data: { ...(n.data as RoutingNodeData), issues: issuesFor(n.id) } }))}
                  edges={graph.edges.map((e) => ({ ...e, markerEnd: { type: MarkerType.ArrowClosed } }))}
                  nodeTypes={nodeTypes}
                  onNodesChange={onNodesChange}
                  onEdgesChange={onEdgesChange}
                  onConnect={onConnect}
                  isValidConnection={isValidConnection}
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
              <div className="flex flex-wrap items-center gap-1 pt-3">
                <div className="inline-flex items-center gap-0.5 rounded-control border border-border bg-surface-muted p-0.5" role="group" aria-label="Add routing nodes">
                  <Button size="sm" variant="ghost" aria-label="Add condition node" title="Add condition" onClick={() => addNode("condition")} className="gap-1.5 px-2">
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M9 18L4 12L9 6" />
                      <path d="M15 6L20 12L15 18" />
                    </svg>
                    <span>Condition</span>
                  </Button>
                  <Button size="sm" variant="ghost" aria-label="Add provider node" title="Add provider" onClick={() => addNode("provider")} className="gap-1.5 px-2">
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <rect x="4" y="4" width="16" height="6" rx="1.5" />
                      <rect x="4" y="14" width="16" height="6" rx="1.5" />
                    </svg>
                    <span>Provider</span>
                  </Button>
                  <Button size="sm" variant="ghost" aria-label="Add modify node" title="Add modify" onClick={() => addNode("modify")} className="gap-1.5 px-2">
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M12 5V19" />
                      <path d="M5 12H19" />
                    </svg>
                    <span>Modify</span>
                  </Button>
                  <Button size="sm" variant="ghost" aria-label="Add respond node" title="Add respond" onClick={() => addNode("respond")} className="gap-1.5 px-2">
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M21 15V6L9 2L3 6V18L9 22L21 18V15" />
                      <path d="M3 6L9 10L15 6" />
                      <path d="M9 10V22" />
                    </svg>
                    <span>Respond</span>
                  </Button>
                </div>
              </div>
              {graph.nodes.length === 0 ? (
                <div className="pt-3">
                  <EmptyState title="No routing graph" body="Load the default graph to start, then add conditions, providers and actions." action={<Button size="sm" variant="secondary" onClick={() => setCurrent(() => initialGraph())}>Load default graph</Button>} />
                </div>
              ) : null}
            </PanelBody>
          </Panel>

          <div className="flex flex-col gap-3">
            <Panel>
              <PanelHeader title={selected ? nodeLabelText(selected) : "Config"} hint={selected ? "Edit node configuration" : "Select a node"} />
              <PanelBody className="flex flex-col gap-3">
                {!selected ? (
                  <EmptyState title="No node selected" body="Click a node to edit its config and connections." />
                ) : (
                  <NodeConfig selected={selected} updateNode={updateNode} deleteSelected={deleteSelected} graph={graph} setCurrent={setCurrent} providerOptions={providerOptions} />
                )}
              </PanelBody>
            </Panel>

            <Panel>
              <PanelHeader title="Validation" hint={issueCountLabel(issues.length)} />
              <PanelBody>
                {issues.length === 0 ? (
                  <p className="text-[13px] text-muted">Graph is valid.</p>
                ) : (
                  <ul className="flex flex-col gap-1 text-[13px] text-failed">
                    {issues.map((i, idx) => (
                      <li key={idx}>
                        • {i.message} {i.nodeId ? `(${issueLabel(i.nodeId)})` : ""}
                      </li>
                    ))}
                  </ul>
                )}
              </PanelBody>
            </Panel>
          </div>
        </div>
      </div>

      <DialogRoot open={compiledOpen} onOpenChange={setCompiledOpen}>
        <DialogContent
          title="Compiled config"
          description="What this graph compiles to for the backend routing config. Demo build, nothing is sent anywhere."
          className="w-[min(880px,calc(100vw-32px))]"
        >
          <CodeBlock code={JSON.stringify(compiled, null, 2)} label="routing.config.json" />
        </DialogContent>
      </DialogRoot>
    </ReactFlowProvider>
  );
}

function NodeConfig({
  selected,
  updateNode,
  deleteSelected,
  graph,
  setCurrent,
  providerOptions,
}: {
  selected: RoutingNode;
  updateNode: (fn: (d: RoutingNodeData) => RoutingNodeData) => void;
  deleteSelected: () => void;
  graph: RoutingGraph;
  setCurrent: (fn: (g: RoutingGraph) => RoutingGraph) => void;
  providerOptions: { value: string; label: string }[];
}) {
  const d = selected.data as RoutingNodeData;
  const otherNodes = graph.nodes.filter((n) => n.id !== selected.id).map((n) => ({ value: n.id, label: nodeLabelText(n) }));

  function setConn(sourceHandle: string, targetId: string | "") {
    setCurrent((g) => {
      let edges = g.edges.filter((e) => !(e.source === selected.id && e.sourceHandle === sourceHandle));
      if (targetId) {
        edges = [...edges, { id: `e-${selected.id}-${sourceHandle}-${targetId}`, source: selected.id, target: targetId, sourceHandle, targetHandle: "in", label: labelForHandle(sourceHandle) } as Edge];
      }
      return { ...g, edges };
    });
  }

  const getTarget = (h: string) => graph.edges.find((e) => e.source === selected.id && e.sourceHandle === h)?.target || "";

  return (
    <div className="flex flex-col gap-3">
      {d.kind === "condition" && (
        <>
          <Field label="Field">
            <SelectControl ariaLabel="Condition field" value={d.condition?.field} onValueChange={(v) => updateNode((dd) => ({ ...dd, condition: { ...(dd.condition ?? { operator: "eq", value: "" }), field: v as ConditionField } }))} options={conditionFieldOptions} className="w-full" />
          </Field>
          <Field label="Operator">
            <SelectControl ariaLabel="Condition operator" value={d.condition?.operator} onValueChange={(v) => updateNode((dd) => ({ ...dd, condition: { ...(dd.condition ?? { field: "amount", value: "" }), operator: v as ConditionOperator } }))} options={operatorOptions(d.condition?.field ?? "amount")} className="w-full" />
          </Field>
          <Field label="Value" htmlFor={d.condition?.field !== "method" && d.condition?.field !== "environment" ? "routing-condition-value" : undefined}>
            {d.condition?.field === "method" ? (
              <SelectControl ariaLabel="Condition value" value={d.condition?.value || undefined} onValueChange={(v) => updateNode((dd) => ({ ...dd, condition: { ...(dd.condition ?? { field: "amount", operator: "eq" }), value: v } }))} options={methodOptions} placeholder="Select method" className="w-full" />
            ) : d.condition?.field === "environment" ? (
              <SelectControl ariaLabel="Condition value" value={d.condition?.value || undefined} onValueChange={(v) => updateNode((dd) => ({ ...dd, condition: { ...(dd.condition ?? { field: "amount", operator: "eq" }), value: v } }))} options={envOptions} placeholder="Select environment" className="w-full" />
            ) : (
              <Input id="routing-condition-value" value={d.condition?.value ?? ""} onChange={(e) => updateNode((dd) => ({ ...dd, condition: { ...(dd.condition ?? { field: "amount", operator: "eq" }), value: e.target.value } }))} placeholder={d.condition?.field === "amount" ? "1000000" : "e.g. BCA"} />
            )}
          </Field>
          <Field label="On match">
            <SelectControl ariaLabel="On match route to" value={getTarget("match")} onValueChange={(v) => setConn("match", v)} options={[{ value: "", label: "Not connected" }, ...otherNodes]} className="w-full" />
          </Field>
          <Field label="On no match">
            <SelectControl ariaLabel="On no match route to" value={getTarget("else")} onValueChange={(v) => setConn("else", v)} options={[{ value: "", label: "Not connected" }, ...otherNodes]} className="w-full" />
          </Field>
        </>
      )}

      {d.kind === "provider" && (
        <>
          <Field label="Provider">
            <SelectControl ariaLabel="Route to provider" value={d.provider?.provider ?? ""} onValueChange={(v) => updateNode((dd) => ({ ...dd, provider: { provider: v } }))} options={providerOptions} className="w-full" />
          </Field>
        </>
      )}

      {d.kind === "entry" && (
        <>
          <Field label="Sends to">
            <SelectControl ariaLabel="Entry sends to" value={getTarget("out")} onValueChange={(v) => setConn("out", v)} options={[{ value: "", label: "Not connected" }, ...otherNodes]} className="w-full" />
          </Field>
        </>
      )}

      {d.kind === "modify" && (
        <>
          <Field label="Description" htmlFor="routing-modify-description">
            <Input id="routing-modify-description" value={d.modify?.description ?? ""} onChange={(e) => updateNode((dd) => ({ ...dd, modify: { ...(dd.modify ?? { metadata: [] }), description: e.target.value } }))} placeholder="Optional description" />
          </Field>
          <div className="flex flex-col gap-2" role="group" aria-labelledby="routing-modify-metadata">
            <p className="text-[12px] font-medium text-secondary" id="routing-modify-metadata">Metadata</p>
            {(d.modify?.metadata ?? []).map((row, i) => (
              <div key={i} className="grid grid-cols-[1fr_1fr_auto] gap-2">
                <Input aria-label={`Metadata key ${i + 1}`} value={row.key} onChange={(e) => updateNode((dd) => ({ ...dd, modify: { ...(dd.modify ?? { description: "" }), metadata: (dd.modify?.metadata ?? []).map((r, j) => (j === i ? { ...r, key: e.target.value } : r)) } }))} placeholder="key" />
                <Input aria-label={`Metadata value ${i + 1}`} value={row.value} onChange={(e) => updateNode((dd) => ({ ...dd, modify: { ...(dd.modify ?? { description: "" }), metadata: (dd.modify?.metadata ?? []).map((r, j) => (j === i ? { ...r, value: e.target.value } : r)) } }))} placeholder="value" />
                <Button size="sm" variant="ghost" aria-label={`Remove metadata field ${i + 1}`} onClick={() => updateNode((dd) => ({ ...dd, modify: { ...(dd.modify ?? { description: "" }), metadata: (dd.modify?.metadata ?? []).filter((_, j) => j !== i) } }))}>
                  Remove
                </Button>
              </div>
            ))}
            <Button size="sm" variant="secondary" onClick={() => updateNode((dd) => ({ ...dd, modify: { ...(dd.modify ?? { description: "" }), metadata: [...(dd.modify?.metadata ?? []), { key: "", value: "" }] } }))}>
              Add field
            </Button>
          </div>
          <Field label="Sends to">
            <SelectControl ariaLabel="Modify sends to" value={getTarget("out")} onValueChange={(v) => setConn("out", v)} options={[{ value: "", label: "Not connected" }, ...otherNodes]} className="w-full" />
          </Field>
        </>
      )}

      {d.kind === "respond" && (
        <>
          <Field label="Status">
            <SelectControl ariaLabel="Response status" value={(d.respond?.status ?? 200).toString()} onValueChange={(v) => updateNode((dd) => ({ ...dd, respond: { ...(dd.respond ?? { body: "{}" }), status: Number(v) } }))} options={statusOptionsRespond} className="w-full" />
          </Field>
          <Field label="Body (JSON)" htmlFor="routing-respond-body" error={d.respond?.body && !isValidJSON(d.respond.body) ? "Not valid JSON" : undefined}>
            <Textarea id="routing-respond-body" value={d.respond?.body ?? "{}"} onChange={(e) => updateNode((dd) => ({ ...dd, respond: { ...(dd.respond ?? { status: 200 }), body: e.target.value } }))} className="font-mono text-[12.5px]" />
          </Field>
        </>
      )}

      {d.kind !== "entry" && (
        <div className="pt-2">
          <Button size="sm" variant="danger" onClick={deleteSelected}>
            Delete node
          </Button>
        </div>
      )}
    </div>
  );
}

function labelForHandle(h?: string | null) {
  if (!h) return "";
  if (h === "match") return "match";
  if (h === "else") return "no match";
  if (h === "out") return "next";
  return h;
}

function nodeLabelText(n: RoutingNode) {
  const d = n.data as RoutingNodeData;
  if (d.kind === "entry") return "Entry";
  if (d.kind === "condition") {
    const c = d.condition;
    return c ? `Condition (${c.field} ${opSym(c.operator)} ${c.value || "no value"})` : "Condition";
  }
  if (d.kind === "provider") return d.provider?.provider || "Provider";
  if (d.kind === "modify") return "Modify request";
  if (d.kind === "respond") return "Respond JSON";
  return d.label;
}