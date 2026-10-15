import type { InputHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={cn(
        "h-8 w-full rounded-control border border-border bg-surface px-2.5 text-[13px] text-ink placeholder:text-muted",
        "disabled:bg-surface-muted disabled:text-muted",
        className,
      )}
      {...props}
    />
  );
}
