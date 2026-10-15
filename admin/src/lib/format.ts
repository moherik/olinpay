const idr = new Intl.NumberFormat("id-ID", {
  style: "currency",
  currency: "IDR",
  maximumFractionDigits: 0,
});

export function formatIDR(amount: number) {
  return idr.format(amount);
}

/** Rupiah without the symbol, for dense table columns. */
export function formatAmount(amount: number) {
  return new Intl.NumberFormat("id-ID", { maximumFractionDigits: 0 }).format(
    amount,
  );
}

export function formatCount(value: number) {
  return new Intl.NumberFormat("id-ID").format(value);
}

/**
 * Fixed locale and time zone so the demo renders the same in every viewer's
 * browser. Server output would otherwise differ from client output.
 */
export function formatDateTime(iso: string) {
  return new Intl.DateTimeFormat("en-GB", {
    timeZone: "Asia/Jakarta",
    day: "2-digit",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(new Date(iso));
}

export function formatRelative(iso: string, now: number) {
  const seconds = Math.round((now - new Date(iso).getTime()) / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}
