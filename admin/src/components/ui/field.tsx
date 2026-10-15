import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export function Field({
  label,
  htmlFor,
  required,
  help,
  error,
  children,
  className,
}: {
  label: string;
  htmlFor?: string;
  required?: boolean;
  help?: string;
  error?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col gap-1", className)}>
      <label
        htmlFor={htmlFor}
        className="flex items-center gap-1 text-[12px] font-medium text-secondary"
      >
        {label}
        {required ? (
          <span className="text-muted">(required)</span>
        ) : null}
      </label>
      {children}
      {help ? <p className="text-[12px] text-muted">{help}</p> : null}
      {error ? (
        <p className="text-[12px] text-failed" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}
