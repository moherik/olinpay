import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/** A dense label and value row. Used instead of a grid of stat cards. */
export function DefRow({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "grid grid-cols-[160px_1fr] items-baseline gap-3 border-b border-border-subtle py-2 last:border-b-0",
        className,
      )}
    >
      <dt className="text-[12px] text-muted">{label}</dt>
      <dd className="min-w-0 text-[13px] text-ink">{children}</dd>
    </div>
  );
}

export function DefList({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return <dl className={cn("flex flex-col", className)}>{children}</dl>;
}

export function Mono({ children }: { children: ReactNode }) {
  return <span className="font-mono text-[12.5px] text-ink">{children}</span>;
}
