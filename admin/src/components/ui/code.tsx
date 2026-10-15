"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";

export function CodeBlock({
  code,
  label,
}: {
  code: string;
  label: string;
}) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }

  return (
    <div className="rounded-panel border border-border bg-surface">
      <div className="flex items-center justify-between border-b border-border-subtle px-3 py-1.5">
        <span className="font-mono text-[12px] text-secondary">{label}</span>
        <Button variant="ghost" size="sm" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <pre className="max-h-[280px] overflow-auto px-3 py-2.5 font-mono text-[12px] leading-[1.6] text-ink">
        {code}
      </pre>
    </div>
  );
}
