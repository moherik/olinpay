import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/*
 * Empty, loading and error states say what is happening and what to do next.
 * "No data" on its own is not a state (R-27).
 */
export function EmptyState({
  title,
  body,
  action,
  className,
}: {
  title: string;
  body: string;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-start gap-2 px-4 py-10 text-left",
        className,
      )}
    >
      <p className="text-[13px] font-medium text-ink">{title}</p>
      <p className="max-w-[52ch] text-[13px] text-secondary">{body}</p>
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}

export function LoadingState({ label }: { label: string }) {
  return (
    <div
      role="status"
      className="flex items-center gap-2 px-4 py-6 text-[13px] text-secondary"
    >
      <span className="inline-block h-3 w-3 rounded-full border-2 border-border border-t-accent motion-safe:animate-spin" />
      {label}
    </div>
  );
}

export function ErrorState({
  title,
  body,
  action,
}: {
  title: string;
  body: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-start gap-2 px-4 py-10">
      <p className="text-[13px] font-medium text-failed">{title}</p>
      <p className="max-w-[52ch] text-[13px] text-secondary">{body}</p>
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}
