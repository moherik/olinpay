export type ActivityTone = "neutral" | "ok" | "warn" | "danger";

export function activityLabel(direction: string, phase: string, method?: string, url?: string, provider?: string): string {
  if (direction === "provider_request" && phase === "request" && url) {
    return `${provider || "Provider"} ${method || "Request"} ${url}`;
  }
  if (phase === "request" && url) return `${method || "Request"} ${url}`;
  const labels: Record<string, string> = {
    "merchant_request:request": "Merchant Request",
    "merchant_response:response": "AwsemPay Response",
    "provider_request:request": "Provider Request",
    "provider_response:response": provider ? `Provider Response · ${provider}` : "Provider Response",
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
