import type { TextareaHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={cn(
        "min-h-[88px] w-full rounded-control border border-border bg-surface px-2.5 py-1.5 text-[13px] text-ink placeholder:text-muted",
        "disabled:bg-surface-muted disabled:text-muted",
        className,
      )}
      {...props}
    />
  );
}