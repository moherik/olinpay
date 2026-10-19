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

/** What a respond node answers: the same shape the payment API's wire format stores. */
export type RespondConfig = { status: number; code: string; message: string };

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

/** One request edit on the wire format the engine reads (domain.RoutingModify). */
export type ModifyItem =
  | { op: "set_metadata"; key: string; value: string }
  | { op: "set_description"; value: string };

export type RejectPayload = { status: number; code: string; message: string };

export type RoutingRulePayload = {
  condition: { field: string; operator: string; value: string };
  providers: string[];
  apply?: ModifyItem[];
  reject?: RejectPayload;
};

export type CompiledRouting = {
  strategy: string;
  providers: { name: string; enabled: boolean; priority: number }[];
  rules: RoutingRulePayload[];
  enrich: ModifyItem[];
  reject: RejectPayload | null;
  issues: Issue[];
};

/**
 * The edge marker the studio draws on every saved edge. Typed as a lookup so
 * importing the value enum is not needed in this type-only module.
 */
const arrowMarker = { type: "arrowclosed" } as unknown as Edge["markerEnd"];

/**
 * Flattens a node's edit form onto the wire format: metadata rows first, then
 * the description. An edit with neither a key/value pair nor a description
 * contributes nothing.
 */
function modifyItems(cfg: ModifyConfig | undefined): ModifyItem[] {
  if (!cfg) return [];
  const out: ModifyItem[] = [];
  (cfg.metadata ?? []).forEach((row) => {
    if (row.key.trim() && row.value !== "") {
      out.push({ op: "set_metadata", key: row.key.trim(), value: row.value });
    }
  });
  if (cfg.description.trim() !== "") {
    out.push({ op: "set_description", value: cfg.description.trim() });
  }
  return out;
}

/** Rebuilds one node's edit form from wire edits (grouped by field). */
function modifyNodeItems(item: ModifyItem): ModifyConfig {
  if (item.op === "set_description") {
    return { description: item.value, metadata: [{ key: "", value: "" }] };
  }
  return { description: "", metadata: [{ key: item.key, value: item.value }] };
}

/** Reads a respond node's answer, or null when it is not storeable as-is. */
function rejectFromRespond(r: RespondConfig | undefined): RejectPayload | null {
  if (!r) return null;
  const status = Number(r.status);
  if (!Number.isInteger(status) || status < 400 || status > 599) return null;
  const code = (r.code ?? "").trim();
  const message = (r.message ?? "").trim();
  if (code === "" || message === "" || /\s/.test(code)) return null;
  return { status, code, message };
}

/**
 * Rebuilds the routing decision tree as a graph: entry node on the left,
 * entry-path modify nodes, one condition node per stored rule (its match branch
 * carries the rule's modify edits, provider chain or respond node, and its else
 * branch continues to the next rule), then the fallback chain, optionally
 * ending in the fallback respond node. The layout mirrors the compile walk so a
 * saved config round-trips visually.
 */
export function graphFromRouting(
  routing: {
    strategy: string;
    providers: { name: string; enabled: boolean }[];
    rules?: {
      condition: { field: string; operator: string; value: string };
      providers: string[];
      apply?: ModifyItem[];
      reject?: RejectPayload;
    }[];
    enrich?: ModifyItem[];
    reject?: RejectPayload | null;
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

  (routing.enrich ?? []).forEach((item) => {
    const node = createNode("modify", { x, y: 200 });
    node.data.modify = modifyNodeItems(item);
    connect(prev, prevHandle, node, "next");
    prev = node.id;
    prevHandle = "out";
    x += 240;
  });

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

    let branch = cond.id;
    let branchHandle = "match";
    (rule.apply ?? []).forEach((item) => {
      const node = createNode("modify", { x, y: 40 });
      node.data.modify = modifyNodeItems(item);
      connect(branch, branchHandle, node, branchHandle === "match" ? "match" : "next");
      branch = node.id;
      branchHandle = "out";
      x += 240;
    });

    if (rule.reject) {
      const node = createNode("respond", { x, y: 40 });
      node.data.respond = { ...rule.reject };
      connect(branch, branchHandle, node, branchHandle === "match" ? "match" : "next");
      x += 240;
      return;
    }

    rule.providers.forEach((name, index) => {
      const node = createNode("provider", { x, y: 40 });
      node.data.provider = { provider: name, enabled: true };
      connect(branch, index === 0 ? branchHandle : "out", node, index === 0 && branchHandle === "match" ? "match" : "next");
      branch = node.id;
      branchHandle = "out";
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

  if (routing.reject) {
    const node = createNode("respond", { x, y: 200 });
    node.data.respond = { ...routing.reject };
    connect(prev, prevHandle, node, prevHandle === "else" ? "no match" : "next");
  }

  return { nodes, edges };
}

/**
 * Compiles the graph back onto the wire format the engine understands.
 *
 * The graph is a decision tree: walking from the entry, modify nodes directly
 * after the entry enrich every request (they run before rule evaluation, so
 * conditions can read the edited values), each condition node contributes one
 * rule whose match branch may carry modify edits (applied only when the rule
 * matches), a provider chain, or a respond node (the rule rejects the payment
 * without touching a gateway), and the "no match" branch continues the walk.
 * Provider nodes on the main path form the fallback chain, optionally ending
 * in a respond node that answers when the whole chain declines.
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
    return { strategy, providers: [], rules: [], enrich: [], reject: null, issues: [...issues, { message: "Graph has no entry node." }] };
  }

  const nodeOf = (id: string) => graph.nodes.find((n) => n.id === id) as RoutingNode | undefined;
  const outsOf = (id: string, handle?: string) =>
    graph.edges.filter((e) => e.source === id && (handle ? e.sourceHandle === handle : true));

  const providers: CompiledRouting["providers"] = [];
  const rules: RoutingRulePayload[] = [];
  const enrich: ModifyItem[] = [];
  let reject: RejectPayload | null = null;
  const visited = new Set<string>();
  const seenConditions = new Set<string>();

  /**
   * Walks one condition's match branch and returns its edits, provider chain
   * and optionally a respond node. Modify nodes must precede the providers;
   * a respond node ends the branch.
   */
  const collectRuleBranch = (startId: string): { chain: string[]; apply: ModifyItem[]; respond: RejectPayload | null } => {
    const chain: string[] = [];
    const apply: ModifyItem[] = [];
    let respond: RejectPayload | null = null;
    let cur: string | undefined = outsOf(startId, "match")[0]?.target;

    while (cur && !visited.has(cur)) {
      visited.add(cur);
      const node = nodeOf(cur);
      if (!node) break;
      const d = node.data as RoutingNodeData;

      if (d.kind === "modify") {
        if (chain.length > 0 || respond) {
          issues.push({ nodeId: node.id, message: "Modify edits in a match branch come between the condition and its providers." });
          break;
        }
        apply.push(...modifyItems(d.modify));
        const next = outsOf(node.id, "out");
        if (next.length > 1) {
          issues.push({ nodeId: node.id, message: "A modify node forwards to only one next step — remove excess edges." });
          break;
        }
        cur = next[0]?.target;
        continue;
      }

      if (d.kind === "provider") {
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
        continue;
      }

      if (d.kind === "respond") {
        respond = rejectFromRespond(d.respond);
        if (!respond) {
          issues.push({ nodeId: node.id, message: "Response needs a status (400..599), a one-word code and a message." });
        }
        if (outsOf(node.id, "out").length > 0) {
          issues.push({ nodeId: node.id, message: "A response ends its branch — remove outgoing edges." });
        }
        break;
      }

      issues.push({ nodeId: node.id, message: "A match branch can carry modify edits, providers or one response — nested conditions are not supported." });
      break;
    }

    return { chain, apply, respond };
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

    if (d.kind === "modify") {
      if (rules.length > 0 || providers.length > 0) {
        issues.push({ nodeId: node.id, message: "Modify nodes must come directly after the entry, before any condition — edits there run before rule evaluation." });
        break;
      }
      const items = modifyItems(d.modify);
      if (items.length === 0) {
        issues.push({ nodeId: node.id, message: "The modify node sets nothing — add an edit or remove it." });
      } else {
        enrich.push(...items);
      }
      const next = outsOf(node.id, "out");
      if (next.length > 1) {
        issues.push({ nodeId: node.id, message: "A modify node forwards to only one next step — remove excess edges." });
        break;
      }
      cur = next[0]?.target;
      continue;
    }

    if (d.kind === "respond") {
      if (providers.length === 0) {
        issues.push({
          nodeId: node.id,
          message: "Connect the response to the end of the fallback chain — the engine rejects unroutable payments before it consults a response.",
        });
        break;
      }
      const parsed = rejectFromRespond(d.respond);
      if (!parsed) {
        issues.push({ nodeId: node.id, message: "Response needs a status (400..599), a one-word code and a message." });
        break;
      }
      reject = parsed;
      if (outsOf(node.id, "out").length > 0) {
        issues.push({ nodeId: node.id, message: "A response ends the graph — remove outgoing edges." });
      }
      break;
    }

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
        issues.push({ nodeId: node.id, message: "Connect the condition's match output to a provider, a response or modify edits." });
      } else {
        const { chain, apply, respond } = collectRuleBranch(node.id);
        if (respond) {
          if (chain.length > 0) {
            issues.push({ nodeId: node.id, message: "A rule rejects or routes, not both — the response and the providers are on the same branch." });
          } else {
            rules.push({ condition: { field, operator: cond?.operator ?? "", value: cond?.value ?? "" }, providers: [], reject: respond });
          }
        } else if (chain.length === 0) {
          issues.push({ nodeId: node.id, message: "The match branch reaches no enabled provider." });
        } else if (valid) {
          rules.push({
            condition: { field, operator: cond?.operator ?? "", value: cond?.value ?? "" },
            providers: chain,
            ...(apply.length > 0 ? { apply } : {}),
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

    issues.push({ nodeId: node.id, message: "Only provider, condition, modify and respond nodes take part in routing." });
    break;
  }

  graph.nodes.forEach((n) => {
    if (n.id !== entry.id && !visited.has(n.id)) {
      issues.push({ nodeId: n.id, message: "Node is not reachable from the entry and would not be saved." });
    }
  });

  return { strategy, providers, rules, enrich, reject, issues };
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

// A response node can never answer 2xx: it replaces an *unaccepted* payment,
// wherever the graph reaches it (a rule branch or the end of the chain).
export const statusOptionsRespond = [
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
        data: { kind: "respond", label: "Respond", respond: { status: 402, code: "", message: "" } },
      };
    default:
      return { id, type: "routing", position, data: { kind: "entry", label: "Entry" }, deletable: false };
  }
}

