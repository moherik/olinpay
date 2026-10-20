import assert from "node:assert/strict";
import test from "node:test";
import {
  continueWithoutKey,
  initialState,
  keyCreated,
  keyFailed,
  merchantCreated,
} from "./setup-state.mjs";

const merchant = {
  id: "AW123456",
  name: "First merchant",
  environment: "sandbox",
  webhookSecret: "whsec_first",
};

test("continuing without a key retains the created merchant and webhook secret", () => {
  const created = merchantCreated(initialState, merchant);
  const failed = keyFailed(created, "The key request could not be confirmed.");
  const complete = continueWithoutKey(failed);

  assert.equal(complete.status, "complete-without-key");
  assert.equal(complete.merchant.id, "AW123456");
  assert.equal(complete.merchant.webhookSecret, "whsec_first");
});

test("a key failure cannot return to merchant creation", () => {
  const created = merchantCreated(initialState, merchant);
  const failed = keyFailed(created, "The key request failed.");

  assert.throws(() => merchantCreated(failed, merchant), TypeError);
});

test("a created key retains both one-time secrets", () => {
  const created = merchantCreated(initialState, merchant);
  const complete = keyCreated(created, "sk_test_first");

  assert.equal(complete.status, "complete-with-key");
  assert.equal(complete.merchant.webhookSecret, "whsec_first");
  assert.equal(complete.apiKey, "sk_test_first");
});

test("reopening a partial merchant step retains its merchant state", () => {
  const created = merchantCreated(initialState, merchant);
  const failed = keyFailed(created, "The key request failed.");

  assert.equal(failed.status, "key-failed");
  assert.deepEqual(failed.merchant, merchant);
});

test("state transitions do not retry or revoke keys", () => {
  const created = merchantCreated(initialState, merchant);
  const failed = keyFailed(created, "The key request failed.");

  assert.throws(
    () => keyFailed(failed, "The key request failed again."),
    TypeError,
  );
  assert.throws(() => keyCreated(failed, "sk_test_second"), TypeError);
});
