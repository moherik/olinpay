"use client";

import * as Menu from "@radix-ui/react-dropdown-menu";
import { IconCheck } from "@/components/icons";
import { cn } from "@/lib/utils";

export const DropdownRoot = Menu.Root;
export const DropdownTrigger = Menu.Trigger;

export function DropdownContent({
  align = "end",
  children,
}: {
  align?: "start" | "end";
  children: React.ReactNode;
}) {
  return (
    <Menu.Portal>
      <Menu.Content
        align={align}
        sideOffset={4}
        className="z-50 min-w-[190px] overflow-hidden rounded-panel border border-border bg-surface p-1 shadow-[var(--shadow-overlay)]"
      >
        {children}
      </Menu.Content>
    </Menu.Portal>
  );
}

export function DropdownItem({
  children,
  onSelect,
  tone = "default",
  disabled,
}: {
  children: React.ReactNode;
  onSelect?: () => void;
  tone?: "default" | "danger";
  disabled?: boolean;
}) {
  return (
    <Menu.Item
      onSelect={onSelect}
      disabled={disabled}
      className={cn(
        "flex cursor-default items-center gap-2 rounded-[3px] px-2 py-1.5 text-[13px] outline-none data-[highlighted]:bg-surface-muted data-[disabled]:text-muted data-[disabled]:opacity-70",
        tone === "danger" ? "text-failed" : "text-ink",
      )}
    >
      {children}
    </Menu.Item>
  );
}

export function DropdownSeparator() {
  return <Menu.Separator className="my-1 h-px bg-border-subtle" />;
}

/*
 * Single choice inside a menu. Radio semantics give every option an
 * aria-checked value, so the selected theme is announced rather than only drawn.
 */
export function DropdownRadioGroup({
  value,
  onValueChange,
  children,
}: {
  value: string;
  onValueChange: (value: string) => void;
  children: React.ReactNode;
}) {
  return (
    <Menu.RadioGroup value={value} onValueChange={onValueChange}>
      {children}
    </Menu.RadioGroup>
  );
}

export function DropdownRadioItem({
  value,
  children,
}: {
  value: string;
  children: React.ReactNode;
}) {
  return (
    <Menu.RadioItem
      value={value}
      className="flex cursor-default items-center gap-2 rounded-[3px] px-2 py-1.5 text-[13px] text-ink outline-none data-[highlighted]:bg-surface-muted"
    >
      <span className="flex-1">{children}</span>
      <span className="inline-flex h-3.5 w-3.5 items-center justify-center text-accent">
        <Menu.ItemIndicator>
          <IconCheck size={14} />
        </Menu.ItemIndicator>
      </span>
    </Menu.RadioItem>
  );
}

export function DropdownLabel({ children }: { children: React.ReactNode }) {
  return (
    <Menu.Label className="px-2 py-1 text-[11px] font-medium tracking-[0.02em] text-muted">
      {children}
    </Menu.Label>
  );
}
