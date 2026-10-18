import type {
  AdminUser,
  ApiKey,
  Delivery,
  Environment,
  InboundEvent,
  Merchant,
  Method,
  Provider,
  Status,
  Transaction,
} from "@/lib/demo-data";

/*
 * Thin typed client for the Go admin API (same-origin /admin/api via the
 * Next.js rewrite). Every response is the shared envelope:
 *   { "data": ..., "meta": { ... } }
 * and every error is:
 *   { "error": { "code", "message", "field"? } }
 */

const BASE_PATH = "/admin/api";

export class ApiError extends Error {
  status: number;
  code: string;
  field?: string;

  constructor(status: number, code: string, message: string, field?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.field = field;
  }
}

type Envelope = {
  data?: unknown;
  meta?: Record<string, unknown>;
  error?: { code: string; message: string; field?: string };
};

async function request<T>(
  path: string,
  init?: RequestInit,
): Promise<{ data: T; meta: Record<string, unknown> }> {
  let method = init?.body ? "POST" : "GET";
  const headers: HeadersInit = { Accept: "application/json" };
  if (init?.method) method = init.method;
  if (init?.body) Object.assign(headers, { "Content-Type": "application/json" });

  const response = await fetch(`${BASE_PATH}${path}`, {
    method,
    credentials: "same-origin",
    cache: "no-store",
    ...init,
    headers,
  });

  let body: Envelope;
  try {
    body = (await response.json()) as Envelope;
  } catch {
    throw new ApiError(
      response.status,
      "bad_response",
      "The admin API returned a non-JSON response.",
    );
  }

  if (!response.ok) {
    const err = body.error;
    throw new ApiError(
      response.status,
      err?.code ?? "error",
      err?.message ?? `Request failed with status ${response.status}.`,
      err?.field,
    );
  }
  return { data: body.data as T, meta: body.meta ?? {} };
}

export function apiGet<T>(
  path: string,
  params?: Record<string, string | number | undefined>,
) {
  const query = params
    ? Object.entries(params)
        .filter(([, value]) => value !== undefined && value !== "")
        .map(
          ([key, value]) =>
            `${encodeURIComponent(key)}=${encodeURIComponent(String(value))}`,
        )
        .join("&")
    : "";
  return request<T>(query ? `${path}?${query}` : path);
}

export function apiMutate<T>(
  method: "POST" | "PUT" | "PATCH" | "DELETE",
  path: string,
  body?: unknown,
) {
  return request<T>(path, {
    method,
    body: body === undefined ? "{}" : JSON.stringify(body),
  });
}

/* ------------------------------------------------------------------ */
/* Backend DTOs (snake_case, straight from Go).                        */
/* ------------------------------------------------------------------ */

export type RawTransaction = {
  id: string;
  merchant_id: string;
  environment: string;
  reference_id: string;
  provider: string;
  method: string;
  channel?: string;
  amount: number;
  currency: string;
  status: string;
  payment_code?: string;
  actions?: {
    type: string;
    value: string;
    description?: string;
  }[];
  customer?: { name?: string; email?: string };
  description?: string;
  metadata?: Record<string, string>;
  failure_reason?: string;
  provider_reference?: string;
  expires_at?: string | null;
  paid_at?: string | null;
  created_at: string;
  updated_at: string;
};

export type RawDelivery = {
  id: string;
  transaction_id: string;
  merchant_id: string;
  environment: string;
  event_type: string;
  url: string;
  payload: string;
  status: string;
  attempts: number;
  max_attempts: number;
  last_error?: string;
  next_attempt_at?: string | null;
  created_at: string;
  updated_at: string;
};

export type RawInbound = {
  id: string;
  provider: string;
  environment: string;
  transaction_id?: string;
  event_type?: string;
  reference_id?: string;
  provider_reference?: string;
  amount?: number;
  status?: string;
  signature_valid: boolean;
  parsed: boolean;
  applied: boolean;
  note?: string;
  remote_addr?: string;
  received_at: string;
};

export type RawApiKey = {
  id: string;
  merchant_id: string;
  label: string;
  key_prefix: string;
  environment: string;
  status: string;
  last_used_at?: string | null;
  created_at: string;
  updated_at: string;
};

export type RawAdminUser = {
  id: string;
  email: string;
  name: string;
  role: "owner" | "operator" | "tech";
  status: "active" | "suspended";
  last_login_at?: string | null;
  created_at: string;
};

export type RawAuditEntry = {
  id: string;
  actor: string;
  action: string;
  target?: string;
  ip?: string;
  at: string;
};

export type RawField = {
  key: string;
  label: string;
  secret?: boolean;
  required?: boolean;
  multiline?: boolean;
  options?: string[];
  placeholder?: string;
  help?: string;
};

export type RawProviderEntry = {
  name: string;
  label: string;
  summary?: string;
  environments: string[];
  methods: string[];
  config_fields: RawField[];
  env_state: Record<
    "sandbox" | "production",
    {
      configured: boolean;
      instance: string;
      active: boolean;
      values: Record<string, string>;
      set_secrets: string[];
      custom_keys: string[];
      updated_at?: string | null;
      updated_by?: string;
    }
  >;
};

export type RawMerchantRow = {
  id: string;
  name: string;
  callback_url: string;
  status: "active" | "inactive" | "suspended";
  created_at: string;
  updated_at: string;
  active_keys: number;
  transactions_30d: number;
  environments: string[];
};

export type RawOverview = {
  total_transactions: number;
  statuses: { status: string; count: number }[];
  created_per_hour: number[];
  failed_per_hour: number[];
  deliveries: { delivered: number; pending: number; failed: number };
  providers: { provider: string; payments: number; failed: number }[];
  attention: {
    failed_deliveries: number;
    unknown_transactions: {
      id: string;
      reference: string;
      provider: string;
      created_at: string;
    }[];
  };
};

/* ------------------------------------------------------------------ */
/* Mappers: backend DTO -> the UI types the pages already render.       */
/* ------------------------------------------------------------------ */

function asEnvironment(raw: string): Environment {
  return raw === "production" ? "production" : "sandbox";
}

export function mapTransaction(raw: RawTransaction): Transaction {
  return {
    id: raw.id,
    reference: raw.reference_id,
    merchant: raw.merchant_id,
    environment: asEnvironment(raw.environment),
    provider: raw.provider,
    method: raw.method as Method,
    channel: raw.channel ?? "",
    amount: raw.amount,
    currency: "IDR",
    status: raw.status as Status,
    createdAt: raw.created_at,
    providerReference: raw.provider_reference ?? "",
    failureReason: raw.failure_reason,
  };
}

export function mapDelivery(raw: RawDelivery): Delivery {
  return {
    id: raw.id,
    event: raw.event_type,
    merchant: raw.merchant_id,
    environment: asEnvironment(raw.environment),
    url: raw.url,
    status: raw.status as Delivery["status"],
    attempts: raw.attempts,
    maxAttempts: raw.max_attempts,
    lastError: raw.last_error || undefined,
    updatedAt: raw.updated_at,
    age: raw.created_at,
    transactionId: raw.transaction_id,
    payload: raw.payload,
  };
}

export function mapInbound(
  raw: RawInbound,
  fallbackEnvironment: Environment,
): InboundEvent {
  return {
    id: raw.id,
    provider: raw.provider,
    environment: raw.environment
      ? asEnvironment(raw.environment)
      : fallbackEnvironment,
    signatureValid: raw.signature_valid,
    parsed: raw.parsed,
    event: raw.event_type || "",
    reference: raw.reference_id || "",
    receivedAt: raw.received_at,
    age: raw.received_at,
    note: raw.note || undefined,
    applied: raw.applied,
    transactionId: raw.transaction_id || undefined,
    environmentKnown: raw.environment !== "",
  };
}

export function mapApiKey(raw: RawApiKey): ApiKey {
  return {
    id: raw.id,
    merchant: raw.merchant_id,
    label: raw.label.length > 0 ? raw.label : "API key",
    prefix: raw.key_prefix,
    environment: asEnvironment(raw.environment),
    createdAt: raw.created_at,
    lastUsedAt: raw.last_used_at ?? null,
    status: raw.status === "revoked" ? "revoked" : "active",
  };
}

export function mapUser(raw: RawAdminUser): AdminUser {
  return {
    id: raw.id,
    email: raw.email,
    name: raw.name,
    role: raw.role,
    status: raw.status,
    lastLoginAt: raw.last_login_at ?? "",
    createdAt: raw.created_at,
  };
}

export function mapMerchant(raw: RawMerchantRow): Merchant {
  return {
    id: raw.id,
    name: raw.name,
    status: raw.status === "active" ? "active" : "suspended",
    environments: (raw.environments ?? []) as Environment[],
    keys: raw.active_keys,
    transactions30d: raw.transactions_30d,
    createdAt: raw.created_at,
    callbackUrl: raw.callback_url,
  };
}

export function mapProvider(
  raw: RawProviderEntry,
  environment: Environment,
): Provider {
  const state = raw.env_state[environment];
  const configured = state?.configured ?? false;
  const active = state?.active ?? false;
  return {
    name: raw.name,
    label: raw.label,
    methods: raw.methods as Method[],
    sandbox: raw.environments.includes("sandbox"),
    production: raw.environments.includes("production"),
    state: active ? "active" : configured ? "configured" : "not_configured",
    environment,
    summary: raw.summary,
    instance: state?.instance ?? raw.name,
    note:
      configured && state?.updated_by
        ? `saved ${state.updated_by ? `by ${state.updated_by}` : ""}`.trim()
        : undefined,
    configFields: (raw.config_fields ?? []).map((field) => ({
      key: field.key,
      // The catalog sets human labels; the key fallback keeps unknown
      // adapters readable instead of rendering a labelless input.
      label: field.label
        ? field.label
        : field.key.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase()),
      type: field.secret ? ("password" as const) : ("text" as const),
      required: field.required ?? false,
      secret: field.secret ?? false,
      multiline: field.multiline ?? false,
      value: field.secret ? undefined : (state?.values?.[field.key] ?? ""),
      options: field.options,
      help: field.help,
    })),
  };
}

/* ------------------------------------------------------------------ */
/* High-level calls used by pages and the shell.                       */
/* ------------------------------------------------------------------ */

export type MeResponse = {
  user: {
    id: string;
    email: string;
    name: string;
    role: AdminUser["role"];
    status: string;
    last_login_at?: string | null;
    created_at: string;
  };
  flags: Record<string, boolean>;
};

export function fetchMe() {
  return apiGet<MeResponse>("/me");
}

export function login(email: string, password: string) {
  return apiMutate<MeResponse>("POST", "/login", { email, password });
}

export function logout() {
  return apiMutate<{ ok: boolean }>("POST", "/logout");
}

export function merchantName(merchantId: string, merchants: Merchant[]) {
  return merchants.find((entry) => entry.id === merchantId)?.name ?? merchantId;
}
