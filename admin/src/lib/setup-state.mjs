export const initialState = Object.freeze({ status: "initial" });

export function hasUnacknowledgedSecrets(state, acknowledged) {
  return !acknowledged && Boolean(state.merchant?.webhookSecret || state.apiKey);
}

function requireStatus(state, expected) {
  if (!state || state.status !== expected) {
    throw new TypeError(`Expected setup state ${expected}.`);
  }
}

function requireMerchant(payload) {
  if (
    !payload ||
    typeof payload.id !== "string" ||
    typeof payload.name !== "string" ||
    typeof payload.environment !== "string" ||
    (payload.webhookSecret !== null && typeof payload.webhookSecret !== "string")
  ) {
    throw new TypeError("Expected a merchant setup payload.");
  }

  return Object.freeze({
    id: payload.id,
    name: payload.name,
    environment: payload.environment,
    webhookSecret: payload.webhookSecret,
  });
}

function requireText(value, name) {
  if (typeof value !== "string" || value.trim() === "") {
    throw new TypeError(`Expected ${name}.`);
  }
  return value;
}

export function merchantCreated(state, payload) {
  requireStatus(state, "initial");
  return Object.freeze({
    status: "merchant-created",
    merchant: requireMerchant(payload),
  });
}

export function keyFailed(state, message) {
  requireStatus(state, "merchant-created");
  return Object.freeze({
    status: "key-failed",
    merchant: state.merchant,
    keyError: requireText(message, "a key error message"),
  });
}

export function keyCreated(state, key) {
  requireStatus(state, "merchant-created");
  return Object.freeze({
    status: "complete-with-key",
    merchant: state.merchant,
    apiKey: requireText(key, "an API key"),
  });
}

export function continueWithoutKey(state) {
  requireStatus(state, "key-failed");
  return Object.freeze({
    status: "complete-without-key",
    merchant: state.merchant,
    keyError: state.keyError,
  });
}
