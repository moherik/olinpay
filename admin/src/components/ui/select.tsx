"use client";

import * as Select from "@radix-ui/react-select";
import { IconCheck, IconChevronDown } from "@/components/icons";
import { cn } from "@/lib/utils";

type Option = { value: string; label: string };

export function SelectControl({
  value,
  onValueChange,
  options,
  placeholder = "Select",
  className,
  ariaLabel,
  id,
  disabled,
}: {
  value?: string;
  onValueChange: (value: string) => void;
  options: Option[];
  placeholder?: string;
  className?: string;
  ariaLabel?: string;
  id?: string;
  disabled?: boolean;
}) {
  return (
    <Select.Root value={value} onValueChange={onValueChange} disabled={disabled}>
      <Select.Trigger
        id={id}
        aria-label={ariaLabel}
        disabled={disabled}
        className={cn(
          "inline-flex h-8 items-center justify-between gap-2 rounded-control border border-border bg-surface px-2.5 text-[13px] text-ink",
          "data-[placeholder]:text-muted",
          "disabled:cursor-not-allowed disabled:bg-surface-muted disabled:text-muted",
          className,
        )}
      >
        <Select.Value placeholder={placeholder} />
        <Select.Icon className="text-muted">
          <IconChevronDown size={14} />
        </Select.Icon>
      </Select.Trigger>
      <Select.Portal>
        <Select.Content
          position="popper"
          sideOffset={4}
          className="z-50 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-panel border border-border bg-surface shadow-[var(--shadow-overlay)]"
        >
          <Select.Viewport className="p-1">
            {options.map((option) => (
              <Select.Item
                key={option.value}
                value={option.value}
                className="flex cursor-default items-center justify-between rounded-[3px] px-2 py-1.5 text-[13px] text-ink outline-none data-[highlighted]:bg-surface-muted"
              >
                <Select.ItemText>{option.label}</Select.ItemText>
                <Select.ItemIndicator>
                  <IconCheck size={13} />
                </Select.ItemIndicator>
              </Select.Item>
            ))}
          </Select.Viewport>
        </Select.Content>
      </Select.Portal>
    </Select.Root>
  );
}
