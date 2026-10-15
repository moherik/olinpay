import type { Status } from "@/lib/demo-data";
import { cn } from "@/lib/utils";

/*
 * Status is a system with one visual language across every screen. Color never
 * carries the state alone: the label is always rendered (R-25).
 */
const styles: Record<Status, string> = {
  paid: "bg-paid-bg text-paid",
  pending: "bg-pending-bg text-pending",
  failed: "bg-failed-bg text-failed",
  expired: "bg-neutral-status-bg text-neutral-status",
  cancelled: "bg-neutral-status-bg text-neutral-status",
  refunded: "bg-refunded-bg text-refunded",
  unknown: "bg-neutral-status-bg text-neutral-status",
};

const labels: Record<Status, string> = {
  paid: "Paid",
  pending: "Pending",
  failed: "Failed",
  expired: "Expired",
  cancelled: "Cancelled",
  refunded: "Refunded",
  unknown: "Unknown",
};

export function StatusTag({ status }: { status: Status }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-[3px] px-1.5 py-0.5 text-[11px] font-medium",
        styles[status],
      )}
    >
      {labels[status]}
    </span>
  );
}

export function Tag({
  children,
  tone = "neutral",
}: {
  children: React.ReactNode;
  tone?: "neutral" | "accent" | "warn" | "danger" | "ok";
}) {
  const tones = {
    neutral: "bg-surface-muted text-secondary",
    accent: "bg-accent-soft text-accent",
    warn: "bg-pending-bg text-pending",
    danger: "bg-failed-bg text-failed",
    ok: "bg-paid-bg text-paid",
  } as const;
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-[3px] px-1.5 py-0.5 text-[11px] font-medium",
        tones[tone],
      )}
    >
      {children}
    </span>
  );
}
