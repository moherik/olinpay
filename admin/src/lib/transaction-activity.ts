export type ActivityTone = "neutral" | "ok" | "warn" | "danger";

export function activityOrder(direction: string, phase: string): number {
  const ranks: Record<string, number> = {
    "merchant_request:request": 0,
    "provider_request:request": 1,
    "provider_response:response": 2,
    "merchant_response:response": 3,
    "provider_webhook:request": 4,
    "provider_webhook:response": 5,
    "merchant_webhook:request": 6,
    "merchant_webhook:response": 7,
  };
  return ranks[`${direction}:${phase}`] ?? 4;
}

export function activityLabel(direction: string, phase: string, method?: string, url?: string): string {
  if (phase === "request" && url) return `${method || "Request"} ${url}`;
  const labels: Record<string, string> = {
    "merchant_request:request": "Merchant Request",
    "merchant_response:response": "AwsemPay Response",
    "provider_request:request": "Provider Request",
    "provider_response:response": "Provider Response",
    "provider_webhook:request": "Incoming request",
    "provider_webhook:response": "AwsemPay Acknowledgement",
    "merchant_webhook:request": "Merchant Webhook",
    "merchant_webhook:response": "Merchant Webhook Response",
  };
  return labels[`${direction}:${phase}`] ?? `${direction} ${phase}`;
}

export function activityBody(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    return body;
  }
}

export function activityTone(statusCode: number | undefined, error?: string): ActivityTone {
  if (error) return "danger";
  if (statusCode === undefined || statusCode === 0) return "neutral";
  if (statusCode >= 500) return "danger";
  if (statusCode >= 400) return "warn";
  return "ok";
}

export function activityOutcome(statusCode?: number, durationMs?: number, error?: string): string {
  if (error) return error;
  const parts: string[] = [];
  if (statusCode) parts.push(`HTTP ${statusCode}`);
  if (durationMs !== undefined && durationMs > 0) parts.push(`${durationMs} ms`);
  return parts.join(" · ") || "Recorded";
}
