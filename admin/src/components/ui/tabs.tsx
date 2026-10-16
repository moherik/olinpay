"use client";

import * as Tabs from "@radix-ui/react-tabs";
import { cn } from "@/lib/utils";

export const TabsRoot = Tabs.Root;

export function TabsList({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <Tabs.List
      className={cn(
        "inline-flex items-center gap-0.5 rounded-control border border-border bg-surface-muted p-0.5",
        className,
      )}
    >
      {children}
    </Tabs.List>
  );
}

export function TabsTrigger({
  value,
  children,
  count,
}: {
  value: string;
  children: React.ReactNode;
  count?: number;
}) {
  return (
    <Tabs.Trigger
      value={value}
      className={cn(
        "group inline-flex items-center gap-1.5 rounded-[3px] px-2.5 py-1 text-[12px] text-secondary transition-colors",
        "hover:text-ink",
        "data-[state=active]:bg-surface data-[state=active]:font-medium data-[state=active]:text-ink data-[state=active]:shadow-[var(--shadow-raised)]",
      )}
    >
      {children}
      {count !== undefined ? (
        <span className="num rounded-[3px] bg-canvas px-1 text-[11px] text-muted group-data-[state=active]:text-secondary">
          {count}
        </span>
      ) : null}
    </Tabs.Trigger>
  );
}

export function TabsContent({
  value,
  className,
  children,
}: {
  value: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <Tabs.Content value={value} className={cn("outline-none", className)}>
      {children}
    </Tabs.Content>
  );
}
