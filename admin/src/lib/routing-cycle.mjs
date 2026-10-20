/**
 * Returns the targets of reachable back edges, ordered by their original node
 * position. An edge to a gray node closes a cycle in the depth-first walk.
 *
 * @param {{ id: string }[]} nodes
 * @param {{ source: string, target: string }[]} edges
 * @param {string} entryID
 * @returns {string[]}
 */
export function cycleNodeIDs(nodes, edges, entryID) {
  const nodeIDs = new Set(nodes.map((node) => node.id));
  const edgesBySource = new Map();
  for (const edge of edges) {
    if (!edgesBySource.has(edge.source)) edgesBySource.set(edge.source, []);
    edgesBySource.get(edge.source).push(edge.target);
  }

  const colors = new Map();
  const cycleTargets = new Set();
  const visit = (nodeID) => {
    colors.set(nodeID, "gray");
    for (const targetID of edgesBySource.get(nodeID) ?? []) {
      if (!nodeIDs.has(targetID)) continue;
      if (colors.get(targetID) === "gray") {
        cycleTargets.add(targetID);
      } else if (colors.get(targetID) !== "black") {
        visit(targetID);
      }
    }
    colors.set(nodeID, "black");
  };

  if (nodeIDs.has(entryID)) visit(entryID);
  return nodes.filter((node) => cycleTargets.has(node.id)).map((node) => node.id);
}
