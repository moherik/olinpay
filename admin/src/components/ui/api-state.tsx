"use client";

import { Button } from "@/components/ui/button";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "@/components/ui/empty-state";

/**
 * Shared states for pages that fetch from the admin API. Every page shows the
 * same loading/error shapes, and an error always offers a retry (R-27: no
 * bare "no data").
 */

export function ApiLoading({ label }: { label: string }) {
  return (
    <div className="border border-border bg-surface">
      <LoadingState label={label} />
    </div>
  );
}

export function ApiFailed({
  title,
  error,
  onRetry,
}: {
  title: string;
  error: unknown;
  onRetry: () => void;
}) {
  const message =
    typeof error === "string" && error.length > 0
      ? error
      : error instanceof Error && error.message
        ? error.message
        : "The admin API is unreachable.";
  return (
    <div className="border border-border bg-surface">
      <ErrorState
        title={title}
        body={message}
        action={
          <Button variant="secondary" size="sm" onClick={onRetry}>
            Try again
          </Button>
        }
      />
    </div>
  );
}

export function NoData({ title, body }: { title: string; body: string }) {
  return (
    <div className="border border-border bg-surface">
      <EmptyState title={title} body={body} />
    </div>
  );
}
