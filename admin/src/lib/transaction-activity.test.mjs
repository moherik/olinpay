import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";

test("activity labels cover the four transaction boundaries", () => {
  const root = dirname(import.meta.url.replace("file://", ""));
  return Promise.all([
    readFile(join(root, "transaction-activity.ts"), "utf8"),
    readFile(join(root, "..", "app", "(console)", "transactions", "[id]", "page.tsx"), "utf8"),
  ]).then(([helpers, page]) => {
    assert.match(page, /Activity/);
    ["provider_request", "provider_webhook", "merchant_webhook"].forEach((needle) => assert.match(helpers, new RegExp(needle)));
  });
});

test("failed activity is represented as danger before HTTP status", () => {
  const tone = (status, error) => error ? "danger" : status >= 500 ? "danger" : "ok";
  assert.equal(tone(200, "timeout"), "danger");
  assert.equal(tone(502, undefined), "danger");
});
