import type { Edge, Node } from "@xyflow/react";

export type NodeKind = "entry" | "condition" | "provider" | "modify" | "respond";

export type ConditionField = "amount" | "method" | "channel" | "currency" | "metadata";
export type ConditionOperator = "gte" | "lte" | "eq" | "neq";

export type ConditionConfig = {
  field: ConditionField;
  operator: ConditionOperator;
  value: string;
  /** Only used when field is "metadata". */
  metadataKey?: string;
};

export type ProviderConfig = { provider: string; enabled?: boolean };

export type ModifyConfig = {
  description: string;
  metadata: { key: string; value: string }[];
};

export type RespondConfig = { status: number; body: string };

export type RoutingNodeData = {
  kind: NodeKind;
  label: string;
  condition?: ConditionConfig;
  provider?: ProviderConfig;
  modify?: ModifyConfig;
  respond?: RespondConfig;
  issues?: string[];
};

export type RoutingNode = Node<RoutingNodeData, "routing">;

export type RoutingGraph = {
  nodes: RoutingNode[];
  edges: Edge[];
};

export type Issue = { nodeId?: string; message: string };

export type RoutingRulePayload = {
  condition: { field: string; operator: string; value: string };
  providers: string[];
};

export type CompiledRouting = {
  strategy: string;
  providers: { name: string; enabled: boolean; priority: number }[];
  rules: RoutingRulePayload[];
  issues: Issue[];
};

/**
 * The edge marker the studio draws on every saved edge. Typed as a lookup so
 * importing the value enum is not needed in this type-only module.
 */
const arrowMarker = { type: "arrowclosed" } as unknown as Edge["markerEnd"];

/**
 * Rebuilds the routing decision tree as a graph: entry node on the left,
 * one condition node per stored rule (match branch feeding the rule's provider
 * chain, else branch continuing to the next rule), then the fallback chain.
 * The layout mirrors the compile walk in reverse so a saved config round-trips
 * visually.
 */
export function graphFromRouting(
  routing: {
    strategy: string;
    providers: { name: string; enabled: boolean }[];
    rules?: { condition: { field: string; operator: string; value: string }; providers: string[] }[];
  },
): RoutingGraph {
  const entry = createNode("entry", { x: 0, y: 200 });
  const nodes: RoutingNode[] = [entry];
  const edges: Edge[] = [];

  const connect = (source: string, sourceHandle: string, target: RoutingNode, label: string) => {
    nodes.push(target);
    edges.push({
      id: `e-${source}-${sourceHandle}-${target.id}`,
      source,
      target: target.id,
      sourceHandle,
      targetHandle: "in",
      label,
      markerEnd: arrowMarker,
    } satisfies Edge);
  };

  let prev = entry.id;
  let prevHandle = "out";
  let x = 250;

  (routing.rules ?? []).forEach((rule) => {
    const cond = createNode("condition", { x, y: 200 });
    const field = rule.condition.field;
    cond.data.condition = {
      field: field.startsWith("metadata:") ? "metadata" : (field as ConditionField),
      operator: rule.condition.operator as ConditionOperator,
      value: rule.condition.value,
      metadataKey: field.startsWith("metadata:") ? field.slice("metadata:".length) : "",
    };
    connect(prev, prevHandle, cond, prevHandle === "else" ? "no match" : "next");
    prev = cond.id;
    prevHandle = "else";
    x += 240;

    rule.providers.forEach((name, index) => {
      const node = createNode("provider", { x, y: 40 });
      node.data.provider = { provider: name, enabled: true };
      connect(prev, index === 0 ? "match" : "out", node, index === 0 ? "match" : "next");
      prev = node.id;
      prevHandle = "out";
      x += 240;
    });
  });

  routing.providers.forEach((p) => {
    const node = createNode("provider", { x, y: 200 });
    node.data.provider = { provider: p.name, enabled: p.enabled };
    connect(prev, prevHandle, node, prevHandle === "else" ? "no match" : "next");
    prev = node.id;
    prevHandle = "out";
    x += 240;
  });

  return { nodes, edges };
}

/**
 * Compiles the graph back onto the wire format the engine understands.
 *
 * The graph is a decision tree: walking from the entry, each condition node
 * contributes one rule (its "match" branch is the rule's provider chain) and
 * the "no match" branch continues the walk; provider nodes on the main path
 * form the fallback chain. The engine evaluates rules first-match-wins and
 * falls through to the fallback chain, so a provider node placed *before* any
 * condition would change order between matching and non-matching payments and
 * is reported as an issue instead of being silently reordered.
 */
export function compileRouting(
  graph: RoutingGraph,
  strategy: string,
  liveInstances: string[],
): CompiledRouting {
  const issues: Issue[] = [];
  if (strategy !== "priority" && strategy !== "round_robin") {
    issues.push({ message: "Strategy must be priority or round_robin." });
  }

  const entry = graph.nodes.find((n) => (n.data as RoutingNodeData).kind === "entry");
  if (!entry) {
    return { strategy, providers: [], rules: [], issues: [...issues, { message: "Graph has no entry node." }] };
  }

  const nodeOf = (id: string) => graph.nodes.find((n) => n.id === id) as RoutingNode | undefined;
  const outsOf = (id: string, handle?: string) =>
    graph.edges.filter((e) => e.source === id && (handle ? e.sourceHandle === handle : true));

  const providers: CompiledRouting["providers"] = [];
  const rules: RoutingRulePayload[] = [];
  const visited = new Set<string>();
  const seenConditions = new Set<string>();

  const collectProviderChain = (startId: string, sourceHandle: string): string[] => {
    const chain: string[] = [];
    let cur: string | undefined = outsOf(startId, sourceHandle)[0]?.target;
    while (cur && !visited.has(cur)) {
      visited.add(cur);
      const node = nodeOf(cur);
      if (!node) break;
      const d = node.data as RoutingNodeData;
      if (d.kind !== "provider") {
        issues.push({ nodeId: node.id, message: "Conditional branches can only reach provider nodes — nested conditions are not supported." });
        break;
      }
      if (!d.provider?.provider) {
        issues.push({ nodeId: node.id, message: "Provider node has no gateway selected." });
      } else if (!liveInstances.includes(d.provider.provider)) {
        issues.push({ nodeId: node.id, message: `${d.provider.provider} is not a live provider instance in this environment.` });
      } else if (d.provider.enabled ?? true) {
        chain.push(d.provider.provider);
      }
      const next = outsOf(node.id, "out");
      if (next.length > 1) {
        issues.push({ nodeId: node.id, message: "A provider forwards to only one next step — remove excess edges." });
        break;
      }
      cur = next[0]?.target;
    }
    return chain;
  };

  let cur: string | undefined = outsOf(entry.id, "out")[0]?.target;
  if (outsOf(entry.id, "out").length !== 1) {
    issues.push({ nodeId: entry.id, message: "Connect the entry to exactly one next step." });
    cur = undefined;
  }

  while (cur && !visited.has(cur)) {
    visited.add(cur);
    const node = nodeOf(cur);
    if (!node) break;
    const d = node.data as RoutingNodeData;

    if (d.kind === "provider") {
      if (!d.provider?.provider) {
        issues.push({ nodeId: node.id, message: "Provider node has no gateway selected." });
      } else if (!liveInstances.includes(d.provider.provider)) {
        issues.push({ nodeId: node.id, message: `${d.provider.provider} is not a live provider instance in this environment.` });
      } else {
        providers.push({
          name: d.provider.provider,
          enabled: d.provider.enabled ?? true,
          priority: providers.length + 1,
        });
      }
      const next = outsOf(node.id, "out");
      if (next.length > 1) {
        issues.push({ nodeId: node.id, message: "A provider forwards to only one next step — remove excess edges." });
        break;
      }
      cur = next[0]?.target;
      continue;
    }

    if (d.kind === "condition") {
      if (providers.length > 0) {
        issues.push({
          nodeId: node.id,
          message: "Conditions must come before the fallback providers — rules are evaluated before the fallback chain.",
        });
      }

      const cond = d.condition;
      const label = `condition ${rules.length + 1}`;
      let field = cond?.field ?? "";
      let valid = true;
      if (field === "metadata") {
        const key = (cond?.metadataKey ?? "").trim();
        if (!/^[A-Za-z0-9_-]+$/.test(key)) {
          issues.push({ nodeId: node.id, message: `${label}: metadata key may only contain letters, digits, "_" and "-".` });
          valid = false;
        }
        field = `metadata:${key}`;
      } else if (!["amount", "method", "channel", "currency"].includes(field)) {
        issues.push({ nodeId: node.id, message: `${label}: unknown condition field.` });
        valid = false;
      }
      if (field === "amount" && !/^\d+$/.test((cond?.value ?? "").replace(/\s/g, ""))) {
        issues.push({ nodeId: node.id, message: `${label}: amount must be an integer (IDR: rupiah).` });
        valid = false;
      } else if (field !== "amount" && (cond?.value ?? "").trim() === "") {
        issues.push({ nodeId: node.id, message: `${label}: value is required.` });
        valid = false;
      }
      const key = `${field}|${cond?.operator}|${cond?.value}`;
      if (seenConditions.has(key)) {
        issues.push({ nodeId: node.id, message: `${label}: an earlier rule has the same condition — it would never fire.` });
      }
      seenConditions.add(key);

      const matchOuts = outsOf(node.id, "match");
      if (matchOuts.length !== 1) {
        issues.push({ nodeId: node.id, message: "Connect the condition's match output to a provider." });
      } else {
        const chain = collectProviderChain(node.id, "match");
        if (chain.length === 0) {
          issues.push({ nodeId: node.id, message: "The match branch reaches no enabled provider." });
        } else if (valid) {
          rules.push({
            condition: { field, operator: cond?.operator ?? "", value: cond?.value ?? "" },
            providers: chain,
          });
        }
      }

      const elseOuts = outsOf(node.id, "else");
      if (elseOuts.length > 1) {
        issues.push({ nodeId: node.id, message: "A condition has one no-match continuation — remove excess edges." });
        break;
      }
      cur = elseOuts[0]?.target;
      continue;
    }

    issues.push({ nodeId: node.id, message: "Only provider and condition nodes take part in routing." });
    break;
  }

  graph.nodes.forEach((n) => {
    const d = n.data as RoutingNodeData;
    if (n.id !== entry.id && !visited.has(n.id) && (d.kind === "provider" || d.kind === "condition")) {
      issues.push({ nodeId: n.id, message: "Node is not reachable from the entry and would not be saved." });
    }
  });

  return { strategy, providers, rules, issues };
}

export const conditionFieldOptions = [
  { value: "amount", label: "Amount" },
  { value: "method", label: "Method" },
  { value: "channel", label: "Channel" },
  { value: "currency", label: "Currency" },
  { value: "metadata", label: "Metadata key" },
];

export const methodOptions = [
  { value: "virtual_account", label: "Virtual Account" },
  { value: "qris", label: "QRIS" },
  { value: "ewallet", label: "EWallet" },
  { value: "retail_outlet", label: "Retail Outlet" },
  { value: "card", label: "Card" },
  { value: "paylater", label: "Paylater" },
  { value: "direct_debit", label: "Direct Debit" },
];

export const envOptions = [
  { value: "sandbox", label: "Sandbox" },
  { value: "production", label: "Production" },
];

export const statusOptionsRespond = [
  { value: "200", label: "200 OK" },
  { value: "400", label: "400 Bad Request" },
  { value: "402", label: "402 Payment Required" },
  { value: "409", label: "409 Conflict" },
  { value: "422", label: "422 Unprocessable" },
  { value: "500", label: "500 Internal Error" },
  { value: "503", label: "503 Service Unavailable" },
];

export function operatorOptions(field: ConditionField): { value: ConditionOperator; label: string }[] {
  if (field === "amount") {
    return [
      { value: "gte", label: "at least" },
      { value: "lte", label: "at most" },
      { value: "eq", label: "equals" },
      { value: "neq", label: "not equals" },
    ];
  }
  return [
    { value: "eq", label: "is" },
    { value: "neq", label: "is not" },
  ];
}

export function opSym(op: ConditionOperator) {
  if (op === "gte") return "≥";
  if (op === "lte") return "≤";
  if (op === "eq") return "=";
  if (op === "neq") return "≠";
  return op;
}

export function isValidJSON(s: string) {
  try {
    JSON.parse(s);
    return true;
  } catch {
    return false;
  }
}

export function createNode(kind: NodeKind, position: { x: number; y: number }): RoutingNode {
  const id = "n_" + Math.random().toString(36).slice(2, 10);
  switch (kind) {
    case "entry":
      return { id, type: "routing", position, data: { kind: "entry", label: "Entry" }, deletable: false };
    case "condition":
      return {
        id,
        type: "routing",
        position,
        data: { kind: "condition", label: "Condition", condition: { field: "amount", operator: "gte", value: "" } },
      };
    case "provider":
      return {
        id,
        type: "routing",
        position,
        data: { kind: "provider", label: "Provider", provider: { provider: "" } },
      };
    case "modify":
      return {
        id,
        type: "routing",
        position,
        data: { kind: "modify", label: "Modify request", modify: { description: "", metadata: [{ key: "", value: "" }] } },
      };
    case "respond":
      return {
        id,
        type: "routing",
        position,
        data: { kind: "respond", label: "Respond JSON", respond: { status: 200, body: "{}" } },
      };
    default:
      return { id, type: "routing", position, data: { kind: "entry", label: "Entry" }, deletable: false };
  }
}

export function initialGraph(): RoutingGraph {
  // Seed: entry -> condition amount>=1000000 -> (match) provider midtrans-snap; (else) modify -> provider xendit
  const nodes: RoutingNode[] = [
    { id: "n1", type: "routing", position: { x: 0, y: 160 }, data: { kind: "entry", label: "Entry" }, deletable: false },
    {
      id: "n2",
      type: "routing",
      position: { x: 280, y: 160 },
      data: { kind: "condition", label: "Condition", condition: { field: "amount", operator: "gte", value: "1000000" } },
    },
    {
      id: "n3",
      type: "routing",
      position: { x: 600, y: 60 },
      data: { kind: "provider", label: "Provider", provider: { provider: "midtrans-snap" } },
    },
    {
      id: "n4",
      type: "routing",
      position: { x: 600, y: 260 },
      data: { kind: "modify", label: "Modify request", modify: { description: "", metadata: [{ key: "acquirer", value: "xendit" }] } },
    },
    {
      id: "n5",
      type: "routing",
      position: { x: 900, y: 260 },
      data: { kind: "provider", label: "Provider", provider: { provider: "xendit" } },
    },
  ];
  const edges: Edge[] = [
    { id: "e-n1-out-n2", source: "n1", target: "n2", sourceHandle: "out", targetHandle: "in", label: "next" },
    { id: "e-n2-match-n3", source: "n2", target: "n3", sourceHandle: "match", targetHandle: "in", label: "match" },
    { id: "e-n2-else-n4", source: "n2", target: "n4", sourceHandle: "else", targetHandle: "in", label: "no match" },
    { id: "e-n4-out-n5", source: "n4", target: "n5", sourceHandle: "out", targetHandle: "in", label: "next" },
  ];
  return { nodes, edges };
}

export function validateGraph(nodes: RoutingNode[], edges: Edge[]): Issue[] {
  const issues: Issue[] = [];
  const entryNodes = nodes.filter((n) => n.data.kind === "entry");
  if (entryNodes.length === 0) issues.push({ message: "The graph has no entry node." });
  if (entryNodes.length > 1) issues.push({ message: "The graph has more than one entry node." });

  // cycle detection
  const adj = new Map<string, string[]>();
  nodes.forEach((n) => adj.set(n.id, []));
  edges.forEach((e) => {
    if (e.source && e.target) {
      adj.get(e.source)?.push(e.target);
    }
  });
  const color = new Map<string, number>(); // 0 white 1 gray 2 black
  nodes.forEach((n) => color.set(n.id, 0));
  const hasCycle = (start: string, path: string[]): boolean => {
    color.set(start, 1);
    path.push(start);
    for (const v of adj.get(start) ?? []) {
      const c = color.get(v);
      if (c === 1 || path.includes(v)) return true;
      if (c === 0 && hasCycle(v, [...path])) return true;
    }
    color.set(start, 2);
    return false;
  };
  const start = entryNodes[0]?.id;
  if (start) {
    for (const n of nodes) {
      const c = color.get(n.id);
      if (c === 0) {
        if (hasCycle(n.id, [])) {
          issues.push({ message: "The graph has a cycle." });
          break;
        }
      }
    }
  } else {
    issues.push({ message: "Graph cannot be validated without an entry." });
  }

  // reachability from entry
  if (start) {
    const visited = new Set<string>();
    const q = [start];
    visited.add(start);
    while (q.length) {
      const cur = q.shift()!;
      for (const nxt of adj.get(cur) ?? []) {
        if (!visited.has(nxt)) {
          visited.add(nxt);
          q.push(nxt);
        }
      }
    }
    nodes.forEach((n) => {
      if (n.id !== start && !visited.has(n.id)) {
        issues.push({ nodeId: n.id, message: "Node is not reachable from the entry." });
      }
    });
  }

  // node configs
  nodes.forEach((n) => {
    const d = n.data as RoutingNodeData;
    if (d.kind === "provider") {
      if (!d.provider?.provider) issues.push({ nodeId: n.id, message: "Provider node has no gateway selected." });
    }
    if (d.kind === "respond") {
      if (!d.respond?.body) {
        issues.push({ nodeId: n.id, message: "Response body is not valid JSON." });
        return;
      }
      if (!isValidJSON(d.respond.body)) {
        issues.push({ nodeId: n.id, message: "Response body is not valid JSON." });
      }
    }
    if (d.kind === "condition") {
      const c = d.condition;
      if (!c?.value) issues.push({ nodeId: n.id, message: "Condition has no value to compare." });
      if (c?.field === "amount") {
        if (c.value && Number.isNaN(Number(c.value))) {
          issues.push({ nodeId: n.id, message: "Amount conditions need a number." });
        }
      }
      const out = edges.filter((e) => e.source === n.id);
      if (!out.some((e) => e.sourceHandle === "match")) {
        issues.push({ nodeId: n.id, message: "Condition output 'match' is not connected." });
      }
      if (!out.some((e) => e.sourceHandle === "else")) {
        issues.push({ nodeId: n.id, message: "Condition output 'no match' is not connected." });
      }
    }
    if (d.kind === "modify") {
      const out = edges.filter((e) => e.source === n.id);
      if (!out.length) issues.push({ nodeId: n.id, message: "Modify output is not connected." });
      const m = d.modify;
      m?.metadata?.forEach((row) => {
        if ((row.key && !row.value) || (!row.key && row.value)) {
          issues.push({ nodeId: n.id, message: "Metadata entry needs both key and value." });
        }
      });
    }
    if (d.kind === "entry") {
      const out = edges.filter((e) => e.source === n.id);
      if (!out.length) issues.push({ nodeId: n.id, message: "Entry output is not connected." });
    }
  });

  // edges targeting entry? cannot via UI, harmless
  return issues;
}

export function compileToRoutingConfig(nodes: RoutingNode[], edges: Edge[]) {
  // derive priority order DFS from entry
  const entry = nodes.find((n) => n.data.kind === "entry")?.id;
  const order: string[] = [];
  if (entry) {
    const visited = new Set<string>();
    const stack = [entry];
    while (stack.length) {
      const cur = stack.pop()!;
      if (visited.has(cur)) continue;
      visited.add(cur);
      const outs = edges.filter((e) => e.source === cur).sort((a, b) => (a.sourceHandle || "").localeCompare(b.sourceHandle || ""));
      // visit in some order; providers collect
      for (const e of outs) {
        if (e.target) stack.push(e.target);
      }
      const curN = nodes.find((n) => n.id === cur);
      if (curN?.data.kind === "provider" && curN.data.provider?.provider) {
        order.push(curN.data.provider.provider);
      }
    }
  }
  // unique
  const seen = new Set<string>();
  const providers = order.filter((p) => (seen.has(p) ? false : (seen.add(p), true))).map((name, i) => ({ name, priority: i + 1, enabled: true }));

  return {
    version: 1,
    strategy: "graph",
    providers,
    nodes: nodes.map((n) => ({ id: n.id, kind: n.data.kind, position: n.position, data: n.data })),
    edges: edges.map((e) => ({ id: e.id, source: e.source, sourceHandle: e.sourceHandle, target: e.target, targetHandle: e.targetHandle, label: e.label })),
  };
}
