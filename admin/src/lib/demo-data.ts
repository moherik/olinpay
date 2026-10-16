/**
 * Demo data for the design review build.
 *
 * None of this is real. It is not read from the API and it does not describe any
 * customer, payment, or account. The console shows a persistent notice so a
 * viewer never mistakes it for live data (R-38).
 *
 * Timestamps are fixed so server and client render the same output.
 */

export type Environment = "sandbox" | "production";

export type Status =
  | "pending"
  | "paid"
  | "expired"
  | "cancelled"
  | "failed"
  | "refunded"
  | "unknown";

export type Method =
  | "virtual_account"
  | "qris"
  | "ewallet"
  | "retail_outlet"
  | "card"
  | "paylater"
  | "direct_debit";

export type Transaction = {
  id: string;
  reference: string;
  merchant: string;
  environment: Environment;
  provider: string;
  method: Method;
  channel: string;
  amount: number;
  currency: "IDR";
  status: Status;
  createdAt: string;
  providerReference: string;
  failureReason?: string;
};

export type Delivery = {
  id: string;
  event: string;
  merchant: string;
  environment: Environment;
  url: string;
  status: "pending" | "delivered" | "failed";
  attempts: number;
  maxAttempts: number;
  lastError?: string;
  updatedAt: string;
  age: string;
};

export type InboundEvent = {
  id: string;
  provider: string;
  environment: Environment;
  signatureValid: boolean;
  parsed: boolean;
  event: string;
  reference: string;
  receivedAt: string;
  age: string;
  note?: string;
};

export type Provider = {
  name: string;
  label: string;
  methods: Method[];
  sandbox: boolean;
  production: boolean;
  state: "active" | "configured" | "not_configured";
  environment: Environment;
  note?: string;
  configFields: {
    key: string;
    label: string;
    type: "text" | "password" | "url" | "select";
    required: boolean;
    secret?: boolean;
    value?: string;
    options?: string[];
    help?: string;
  }[];
};

export type AdminUser = {
  id: string;
  email: string;
  name: string;
  role: "owner" | "operator" | "tech";
  status: "active" | "suspended";
  lastLoginAt: string;
};

export const NOW_ISO = "2026-10-15T09:30:00Z";

export type Merchant = {
  id: string;
  name: string;
  status: "active" | "suspended";
  environments: Environment[];
  keys: number;
  transactions30d: number;
  createdAt: string;
};

export const merchants: Merchant[] = [
  {
    id: "mch_demo_a",
    name: "Demo Merchant A",
    status: "active" as const,
    environments: ["sandbox", "production"] as Environment[],
    keys: 3,
    transactions30d: 4820,
    createdAt: "2026-02-11T04:00:00Z",
  },
  {
    id: "mch_demo_b",
    name: "Demo Merchant B",
    status: "active" as const,
    environments: ["sandbox"] as Environment[],
    keys: 1,
    transactions30d: 640,
    createdAt: "2026-05-02T04:00:00Z",
  },
  {
    id: "mch_demo_c",
    name: "Demo Merchant C",
    status: "suspended" as const,
    environments: ["sandbox", "production"] as Environment[],
    keys: 2,
    transactions30d: 0,
    createdAt: "2026-06-19T04:00:00Z",
  },
  {
    id: "mch_demo_d",
    name: "Demo Merchant D",
    status: "active" as const,
    environments: ["production"] as Environment[],
    keys: 1,
    transactions30d: 118,
    createdAt: "2026-09-30T04:00:00Z",
  },
];

export const transactions: Transaction[] = [
  {
    id: "trx_01",
    reference: "DEMO-000181",
    merchant: "Demo Merchant A",
    environment: "production",
    provider: "midtrans-snap",
    method: "virtual_account",
    channel: "BCA",
    amount: 1_250_000,
    currency: "IDR",
    status: "paid",
    createdAt: "2026-10-15T08:52:00Z",
    providerReference: "VA-8820031122",
  },
  {
    id: "trx_02",
    reference: "DEMO-000180",
    merchant: "Demo Merchant A",
    environment: "production",
    provider: "xendit",
    method: "qris",
    channel: "QRIS",
    amount: 87_500,
    currency: "IDR",
    status: "pending",
    createdAt: "2026-10-15T08:47:00Z",
    providerReference: "pr_demo_88120",
  },
  {
    id: "trx_03",
    reference: "DEMO-000179",
    merchant: "Demo Merchant B",
    environment: "sandbox",
    provider: "midtrans",
    method: "ewallet",
    channel: "GOPAY",
    amount: 45_000,
    currency: "IDR",
    status: "failed",
    createdAt: "2026-10-15T08:41:00Z",
    providerReference: "demo-order-88119",
    failureReason: "Gateway declined: insufficient wallet balance",
  },
  {
    id: "trx_04",
    reference: "DEMO-000178",
    merchant: "Demo Merchant A",
    environment: "production",
    provider: "doku",
    method: "virtual_account",
    channel: "MANDIRI",
    amount: 3_400_000,
    currency: "IDR",
    status: "paid",
    createdAt: "2026-10-15T08:12:00Z",
    providerReference: "VA-7710294410",
  },
  {
    id: "trx_05",
    reference: "DEMO-000177",
    merchant: "Demo Merchant D",
    environment: "production",
    provider: "midtrans-snap",
    method: "retail_outlet",
    channel: "ALFAMART",
    amount: 199_000,
    currency: "IDR",
    status: "expired",
    createdAt: "2026-10-15T07:58:00Z",
    providerReference: "ALF-2291043",
  },
  {
    id: "trx_06",
    reference: "DEMO-000176",
    merchant: "Demo Merchant B",
    environment: "sandbox",
    provider: "mock",
    method: "virtual_account",
    channel: "MOCK_VA",
    amount: 150_000,
    currency: "IDR",
    status: "paid",
    createdAt: "2026-10-15T07:44:00Z",
    providerReference: "mock-ref-88116",
  },
  {
    id: "trx_07",
    reference: "DEMO-000175",
    merchant: "Demo Merchant A",
    environment: "production",
    provider: "xendit",
    method: "paylater",
    channel: "KREDIVO",
    amount: 780_000,
    currency: "IDR",
    status: "cancelled",
    createdAt: "2026-10-15T07:30:00Z",
    providerReference: "pr_demo_88115",
  },
  {
    id: "trx_08",
    reference: "DEMO-000174",
    merchant: "Demo Merchant A",
    environment: "production",
    provider: "winpay",
    method: "qris",
    channel: "QRIS",
    amount: 62_000,
    currency: "IDR",
    status: "paid",
    createdAt: "2026-10-15T07:11:00Z",
    providerReference: "WP-99120",
  },
  {
    id: "trx_09",
    reference: "DEMO-000173",
    merchant: "Demo Merchant D",
    environment: "production",
    provider: "midtrans-snap",
    method: "ewallet",
    channel: "DANA",
    amount: 25_000,
    currency: "IDR",
    status: "refunded",
    createdAt: "2026-10-15T06:52:00Z",
    providerReference: "DANA-1120994",
  },
  {
    id: "trx_10",
    reference: "DEMO-000172",
    merchant: "Demo Merchant B",
    environment: "sandbox",
    provider: "midtrans",
    method: "virtual_account",
    channel: "BNI",
    amount: 910_000,
    currency: "IDR",
    status: "pending",
    createdAt: "2026-10-15T06:33:00Z",
    providerReference: "demo-order-88112",
  },
  {
    id: "trx_11",
    reference: "DEMO-000171",
    merchant: "Demo Merchant A",
    environment: "production",
    provider: "doku",
    method: "ewallet",
    channel: "SHOPEEPAY",
    amount: 120_000,
    currency: "IDR",
    status: "unknown",
    createdAt: "2026-10-15T06:04:00Z",
    providerReference: "DD-5510233",
    failureReason: "Gateway outcome ambiguous, awaiting reconciliation",
  },
  {
    id: "trx_12",
    reference: "DEMO-000170",
    merchant: "Demo Merchant A",
    environment: "production",
    provider: "midtrans-snap",
    method: "virtual_account",
    channel: "BRI",
    amount: 4_500_000,
    currency: "IDR",
    status: "paid",
    createdAt: "2026-10-15T05:41:00Z",
    providerReference: "VA-8820030110",
  },
];

export const deliveries: Delivery[] = [
  {
    id: "whd_01",
    event: "payment.paid",
    merchant: "Demo Merchant A",
    environment: "production",
    url: "https://merchant-a.example/callbacks/openpay",
    status: "failed",
    attempts: 6,
    maxAttempts: 6,
    lastError: "connection refused after 10s",
    updatedAt: "2026-10-15T09:02:00Z",
    age: "28m ago",
  },
  {
    id: "whd_02",
    event: "payment.pending",
    merchant: "Demo Merchant B",
    environment: "sandbox",
    url: "https://merchant-b.example/webhooks",
    status: "pending",
    attempts: 2,
    maxAttempts: 6,
    lastError: "503 from recipient",
    updatedAt: "2026-10-15T09:18:00Z",
    age: "12m ago",
  },
  {
    id: "whd_03",
    event: "payment.paid",
    merchant: "Demo Merchant D",
    environment: "production",
    url: "https://merchant-d.example/payments/hook",
    status: "delivered",
    attempts: 1,
    maxAttempts: 6,
    updatedAt: "2026-10-15T08:59:00Z",
    age: "31m ago",
  },
  {
    id: "whd_04",
    event: "payment.expired",
    merchant: "Demo Merchant A",
    environment: "production",
    url: "https://merchant-a.example/callbacks/openpay",
    status: "delivered",
    attempts: 1,
    maxAttempts: 6,
    updatedAt: "2026-10-15T08:54:00Z",
    age: "36m ago",
  },
  {
    id: "whd_05",
    event: "payment.failed",
    merchant: "Demo Merchant B",
    environment: "sandbox",
    url: "https://merchant-b.example/webhooks",
    status: "pending",
    attempts: 3,
    maxAttempts: 6,
    lastError: "timeout after 10s",
    updatedAt: "2026-10-15T09:21:00Z",
    age: "9m ago",
  },
  {
    id: "whd_06",
    event: "payment.refunded",
    merchant: "Demo Merchant D",
    environment: "production",
    url: "https://merchant-d.example/payments/hook",
    status: "delivered",
    attempts: 1,
    maxAttempts: 6,
    updatedAt: "2026-10-15T08:31:00Z",
    age: "59m ago",
  },
  {
    id: "whd_07",
    event: "payment.paid",
    merchant: "Demo Merchant A",
    environment: "production",
    url: "https://merchant-a.example/callbacks/openpay",
    status: "failed",
    attempts: 6,
    maxAttempts: 6,
    lastError: "TLS certificate expired",
    updatedAt: "2026-10-15T07:58:00Z",
    age: "1h ago",
  },
];

export const inboundEvents: InboundEvent[] = [
  {
    id: "whe_01",
    provider: "midtrans-snap",
    environment: "production",
    signatureValid: true,
    parsed: true,
    event: "payment.paid",
    reference: "DEMO-000181",
    receivedAt: "2026-10-15T08:52:04Z",
    age: "38m ago",
  },
  {
    id: "whe_02",
    provider: "xendit",
    environment: "production",
    signatureValid: true,
    parsed: true,
    event: "payment.pending",
    reference: "DEMO-000180",
    receivedAt: "2026-10-15T08:47:11Z",
    age: "43m ago",
  },
  {
    id: "whe_03",
    provider: "doku",
    environment: "production",
    signatureValid: false,
    parsed: false,
    event: "unknown",
    reference: "DEMO-000178",
    receivedAt: "2026-10-15T08:14:02Z",
    age: "1h ago",
    note: "Signature did not match. Callback rejected.",
  },
  {
    id: "whe_04",
    provider: "winpay",
    environment: "production",
    signatureValid: true,
    parsed: true,
    event: "payment.paid",
    reference: "DEMO-000174",
    receivedAt: "2026-10-15T07:11:33Z",
    age: "2h ago",
  },
  {
    id: "whe_05",
    provider: "mock",
    environment: "sandbox",
    signatureValid: true,
    parsed: true,
    event: "payment.paid",
    reference: "DEMO-000176",
    receivedAt: "2026-10-15T07:44:19Z",
    age: "1h ago",
  },
  {
    id: "whe_06",
    provider: "midtrans",
    environment: "sandbox",
    signatureValid: true,
    parsed: false,
    event: "unknown",
    reference: "DEMO-000172",
    receivedAt: "2026-10-15T06:35:47Z",
    age: "3h ago",
    note: "Unrecognized status code from gateway.",
  },
];

export const providers: Provider[] = [
  {
    name: "mock",
    label: "Mock gateway",
    methods: ["virtual_account"],
    sandbox: true,
    production: false,
    state: "active",
    environment: "sandbox",
    note: "Reference adapter for local development. No credentials required.",
    configFields: [
      {
        key: "secret",
        label: "Callback secret",
        type: "password",
        required: true,
        secret: true,
        help: "Used to verify the HMAC signature on inbound callbacks.",
      },
    ],
  },
  {
    name: "midtrans-snap",
    label: "Midtrans BI-SNAP",
    methods: ["virtual_account", "qris", "ewallet"],
    sandbox: true,
    production: true,
    state: "configured",
    environment: "production",
    note: "RSA get-token plus HMAC-SHA512 transaction signature.",
    configFields: [
      { key: "client_id", label: "Client ID", type: "text", required: true, value: "MCH-DEMO-0001" },
      { key: "client_secret", label: "Client secret", type: "password", required: true, secret: true },
      { key: "private_key", label: "Private key (PEM or base64 DER)", type: "password", required: true, secret: true },
      { key: "public_key", label: "Midtrans public key", type: "password", required: true, secret: true, help: "Used to verify inbound notification signatures. Empty rejects callbacks." },
      { key: "merchant_id", label: "Merchant ID", type: "text", required: true, value: "MCH-DEMO-0001" },
      { key: "partner_service_id", label: "Partner service ID (8-char BIN)", type: "text", required: true, value: "   19008" },
      { key: "base_url", label: "Base URL", type: "select", required: true, options: ["https://merchants.sbx.midtrans.com", "https://merchants.midtrans.com"], value: "https://merchants.midtrans.com" },
    ],
  },
  {
    name: "midtrans",
    label: "Midtrans Core API",
    methods: ["virtual_account", "qris", "ewallet", "retail_outlet"],
    sandbox: true,
    production: true,
    state: "configured",
    environment: "sandbox",
    note: "Legacy Core API. Addressed by order id, HTTP Basic auth.",
    configFields: [
      { key: "server_key", label: "Server key", type: "password", required: true, secret: true, help: "Basic-auth password and notification signature key." },
      { key: "base_url", label: "Base URL", type: "select", required: true, options: ["https://api.sandbox.midtrans.com", "https://api.midtrans.com"], value: "https://api.sandbox.midtrans.com" },
    ],
  },
  {
    name: "xendit",
    label: "Xendit Payments v3",
    methods: ["virtual_account", "qris", "ewallet", "retail_outlet", "paylater", "card"],
    sandbox: true,
    production: true,
    state: "configured",
    environment: "sandbox",
    note: "Test and live share the host and are selected by the key prefix.",
    configFields: [
      { key: "secret_key", label: "Secret key", type: "password", required: true, secret: true, help: "xnd_development_ or xnd_production_ prefix." },
      { key: "callback_token", label: "Callback token", type: "password", required: true, secret: true, help: "Shared token compared on inbound callbacks." },
      { key: "base_url", label: "Base URL", type: "url", required: true, value: "https://api.xendit.co" },
    ],
  },
  {
    name: "winpay",
    label: "Winpay SNAP",
    methods: ["virtual_account", "qris", "ewallet"],
    sandbox: true,
    production: false,
    state: "configured",
    environment: "sandbox",
    configFields: [
      { key: "partner_id", label: "Partner ID", type: "text", required: true },
      { key: "private_key", label: "Private key", type: "password", required: true, secret: true },
      { key: "public_key", label: "Public key", type: "password", required: true, secret: true },
      { key: "base_url", label: "Base URL", type: "url", required: true, value: "https://sandbox-snap.winpay.id" },
    ],
  },
  {
    name: "doku",
    label: "DOKU SNAP",
    methods: ["virtual_account", "qris", "ewallet"],
    sandbox: true,
    production: true,
    state: "not_configured",
    environment: "production",
    note: "Correlation identifiers are cached per process today; persistence is planned.",
    configFields: [
      { key: "client_id", label: "Client ID", type: "text", required: true },
      { key: "client_secret", label: "Client secret", type: "password", required: true, secret: true },
      { key: "private_key", label: "Private key", type: "password", required: true, secret: true },
      { key: "partner_service_id", label: "Partner service ID", type: "text", required: true },
      { key: "base_url", label: "Base URL", type: "select", required: true, options: ["https://api-sandbox.doku.com", "https://api.doku.com"], value: "https://api.doku.com" },
    ],
  },
];

export const adminUsers: AdminUser[] = [
  {
    id: "usr_01",
    email: "owner@example.com",
    name: "Admin Owner (demo)",
    role: "owner",
    status: "active",
    lastLoginAt: "2026-10-15T08:02:00Z",
  },
  {
    id: "usr_02",
    email: "operator@example.com",
    name: "Admin Operator (demo)",
    role: "operator",
    status: "active",
    lastLoginAt: "2026-10-15T09:11:00Z",
  },
  {
    id: "usr_03",
    email: "tech@example.com",
    name: "Admin Tech (demo)",
    role: "tech",
    status: "active",
    lastLoginAt: "2026-10-15T07:40:00Z",
  },
  {
    id: "usr_04",
    email: "operator2@example.com",
    name: "Admin Operator 2 (demo)",
    role: "operator",
    status: "suspended",
    lastLoginAt: "2026-09-28T03:22:00Z",
  },
];

export const auditLog = [
  {
    id: "aud_01",
    actor: "owner@example.com",
    action: "provider_credentials.updated",
    target: "midtrans-snap / production",
    at: "2026-10-15T08:41:00Z",
    ip: "10.0.4.12",
  },
  {
    id: "aud_02",
    actor: "operator@example.com",
    action: "transaction.cancel_requested",
    target: "DEMO-000175",
    at: "2026-10-15T07:31:00Z",
    ip: "10.0.4.31",
  },
  {
    id: "aud_03",
    actor: "tech@example.com",
    action: "webhook_delivery.retried",
    target: "whd_01",
    at: "2026-10-15T07:22:00Z",
    ip: "10.0.4.55",
  },
  {
    id: "aud_04",
    actor: "owner@example.com",
    action: "api_key.revoked",
    target: "Demo Merchant C / production",
    at: "2026-10-14T10:05:00Z",
    ip: "10.0.4.12",
  },
  {
    id: "aud_05",
    actor: "operator@example.com",
    action: "merchant.suspended",
    target: "Demo Merchant C",
    at: "2026-10-14T09:58:00Z",
    ip: "10.0.4.31",
  },
  {
    id: "aud_06",
    actor: "owner@example.com",
    action: "user.role_changed",
    target: "operator2@example.com to suspended",
    at: "2026-09-28T03:25:00Z",
    ip: "10.0.4.12",
  },
];

/**
 * Sample series for the two overview charts. Counts only, no rates, so nothing
 * here implies a measure the platform cannot yet compute.
 */
export const paymentsPerHour = [
  6, 4, 3, 2, 2, 3, 5, 9, 14, 21, 26, 24, 19, 22, 28, 31, 27, 24, 30, 34, 29, 22, 15, 9,
];

export const failedPerHour = [
  0, 0, 0, 0, 0, 0, 1, 1, 2, 3, 2, 4, 2, 1, 3, 2, 4, 2, 1, 3, 2, 1, 1, 0,
];

/**
 * Provider split of the same sample set as `statusBreakdown`, so the reliability
 * table, the status rows and the hourly chart all reconcile on the overview.
 */
export const providerReliability = [
  { provider: "midtrans-snap", payments: 772, failed: 16 },
  { provider: "xendit", payments: 648, failed: 19 },
  { provider: "midtrans", payments: 420, failed: 9 },
  { provider: "doku", payments: 262, failed: 16 },
  { provider: "winpay", payments: 145, failed: 6 },
  { provider: "mock", payments: 29, failed: 0 },
];

export const statusBreakdown: { status: Status; count: number }[] = [
  { status: "paid", count: 1842 },
  { status: "pending", count: 214 },
  { status: "expired", count: 96 },
  { status: "failed", count: 66 },
  { status: "cancelled", count: 41 },
  { status: "refunded", count: 12 },
  { status: "unknown", count: 5 },
];

export const methodMix: { method: Method; count: number }[] = [
  { method: "virtual_account", count: 1024 },
  { method: "qris", count: 588 },
  { method: "ewallet", count: 431 },
  { method: "retail_outlet", count: 142 },
  { method: "paylater", count: 51 },
  { method: "card", count: 24 },
  { method: "direct_debit", count: 4 },
];

export type MerchantProvider = {
  provider: string;
  environment: Environment;
  state: "active" | "configured";
  priority: number;
};

export const merchantProviders: Record<string, MerchantProvider[]> = {
  mch_demo_a: [
    { provider: "midtrans-snap", environment: "production", state: "active", priority: 1 },
    { provider: "xendit", environment: "production", state: "active", priority: 2 },
    { provider: "doku", environment: "production", state: "configured", priority: 3 },
    { provider: "winpay", environment: "sandbox", state: "configured", priority: 1 },
  ],
  mch_demo_b: [
    { provider: "midtrans", environment: "sandbox", state: "active", priority: 1 },
    { provider: "mock", environment: "sandbox", state: "active", priority: 2 },
  ],
  mch_demo_c: [],
  mch_demo_d: [
    { provider: "midtrans-snap", environment: "production", state: "active", priority: 1 },
  ],
};

export type ApiKey = {
  id: string;
  merchant: string;
  label: string;
  prefix: string;
  environment: Environment;
  createdAt: string;
  lastUsedAt: string | null;
  status: "active" | "revoked";
};

export const apiKeys: ApiKey[] = [
  {
    id: "key_01",
    merchant: "Demo Merchant A",
    label: "Checkout production",
    prefix: "sk_live_8f21",
    environment: "production",
    createdAt: "2026-03-04T04:00:00Z",
    lastUsedAt: "2026-10-15T09:28:00Z",
    status: "active",
  },
  {
    id: "key_02",
    merchant: "Demo Merchant A",
    label: "Checkout production (rotating)",
    prefix: "sk_live_4c07",
    environment: "production",
    createdAt: "2026-08-19T04:00:00Z",
    lastUsedAt: "2026-10-14T11:02:00Z",
    status: "active",
  },
  {
    id: "key_03",
    merchant: "Demo Merchant A",
    label: "Sandbox",
    prefix: "sk_test_21bd",
    environment: "sandbox",
    createdAt: "2026-03-04T04:00:00Z",
    lastUsedAt: "2026-10-12T07:41:00Z",
    status: "active",
  },
  {
    id: "key_04",
    merchant: "Demo Merchant B",
    label: "Sandbox",
    prefix: "sk_test_9032",
    environment: "sandbox",
    createdAt: "2026-05-02T04:00:00Z",
    lastUsedAt: "2026-10-15T08:41:00Z",
    status: "active",
  },
  {
    id: "key_05",
    merchant: "Demo Merchant C",
    label: "Production",
    prefix: "sk_live_77aa",
    environment: "production",
    createdAt: "2026-06-19T04:00:00Z",
    lastUsedAt: "2026-09-30T02:12:00Z",
    status: "revoked",
  },
  {
    id: "key_06",
    merchant: "Demo Merchant D",
    label: "Production",
    prefix: "sk_live_1e55",
    environment: "production",
    createdAt: "2026-09-30T04:00:00Z",
    lastUsedAt: "2026-10-15T07:58:00Z",
    status: "active",
  },
];

