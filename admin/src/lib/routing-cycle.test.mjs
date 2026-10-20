import assert from "node:assert/strict";
import test from "node:test";

import { cycleNodeIDs } from "./routing-cycle.mjs";

const nodes = (...ids) => ids.map((id) => ({ id }));
const edge = (source, target) => ({ source, target });

test("reports the fallback loop target", () => {
  const result = cycleNodeIDs(
    nodes("entry", "fallback-a", "fallback-b"),
    [edge("entry", "fallback-a"), edge("fallback-a", "fallback-b"), edge("fallback-b", "fallback-a")],
    "entry",
  );

  assert.deepEqual(result, ["fallback-a"]);
});

test("reports the match branch loop target", () => {
  const result = cycleNodeIDs(
    nodes("entry", "condition", "match-a", "match-b", "fallback"),
    [
      edge("entry", "condition"),
      edge("condition", "match-a"),
      edge("match-a", "match-b"),
      edge("match-b", "match-a"),
      edge("condition", "fallback"),
    ],
    "entry",
  );

  assert.deepEqual(result, ["match-a"]);
});

test("reports the no-match branch loop target", () => {
  const result = cycleNodeIDs(
    nodes("entry", "condition-a", "condition-b", "match"),
    [
      edge("entry", "condition-a"),
      edge("condition-a", "condition-b"),
      edge("condition-b", "condition-a"),
      edge("condition-a", "match"),
    ],
    "entry",
  );

  assert.deepEqual(result, ["condition-a"]);
});

test("reports each target in a cross-branch cycle once in node order", () => {
  const result = cycleNodeIDs(
    nodes("entry", "condition", "match", "fallback", "shared"),
    [
      edge("entry", "condition"),
      edge("condition", "match"),
      edge("match", "shared"),
      edge("condition", "fallback"),
      edge("fallback", "shared"),
      edge("shared", "condition"),
    ],
    "entry",
  );

  assert.deepEqual(result, ["condition"]);
});

test("ignores a cycle outside the entry's reachable graph", () => {
  const result = cycleNodeIDs(
    nodes("entry", "fallback", "orphan-a", "orphan-b"),
    [edge("entry", "fallback"), edge("orphan-a", "orphan-b"), edge("orphan-b", "orphan-a")],
    "entry",
  );

  assert.deepEqual(result, []);
});

test("does not report an acyclic graph with a shared tail", () => {
  const result = cycleNodeIDs(
    nodes("entry", "condition", "match", "fallback", "tail"),
    [
      edge("entry", "condition"),
      edge("condition", "match"),
      edge("condition", "fallback"),
      edge("match", "tail"),
      edge("fallback", "tail"),
    ],
    "entry",
  );

  assert.deepEqual(result, []);
});
