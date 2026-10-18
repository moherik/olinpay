"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { AppShell } from "@/components/shell/app-shell";
import { useApp } from "@/components/shell/app-context";
import { LoadingState } from "@/components/ui/empty-state";
import { ApiError } from "@/lib/api";

/**
 * Auth gate for every console page: the session is verified server-side via
 * /api/me before the shell renders. Unauthenticated visitors land on /login.
 */
export default function ConsoleLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const router = useRouter();
  const { auth, refreshAuth, reloadMerchants } = useApp();
  const [gateFailed, setGateFailed] = useState<string | null>(null);
  // The conditional keeps the AuthState union narrowed for the effect
  // dependency arrays, which evaluate outside any branch.
  const sessionActive = auth.ready ? auth.authenticated : false;

  // refreshAuth reports a missing session as state, not as an error; the
  // redirect on expiry happens here.
  useEffect(() => {
    if (auth.ready && !sessionActive) {
      router.replace("/login");
    }
  }, [auth.ready, sessionActive, router]);

  // A session that was verified before this layout mounted (login bounce)
  // still needs the merchant list.
  useEffect(() => {
    if (!sessionActive) return;
    // Deferred a tick so context updates do not run during the effect's
    // commit.
    const timer = setTimeout(() => {
      void reloadMerchants().catch(() => {
        /* pages show their own error states and can retry */
      });
    }, 0);
    return () => clearTimeout(timer);
  }, [sessionActive, reloadMerchants]);

  useEffect(() => {
    if (auth.ready) return;
    let cancelled = false;
    (async () => {
      try {
        await refreshAuth();
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) {
          router.replace("/login");
          return;
        }
        if (!cancelled) {
          setGateFailed("Cannot reach the admin API. Is the backend running?");
        }
        return;
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [auth.ready, refreshAuth, reloadMerchants, router]);

  if (gateFailed) {
    return (
      <div className="flex min-h-dvh items-center justify-center p-6">
        <p className="max-w-sm text-center text-[13px] text-secondary">
          {gateFailed}
        </p>
      </div>
    );
  }

  if (!auth.ready) {
    return (
      <div className="flex min-h-dvh items-center justify-center">
        <LoadingState label="Checking your session…" />
      </div>
    );
  }

  if (!auth.authenticated) {
    return (
      <div className="flex min-h-dvh items-center justify-center">
        <LoadingState label="Session expired — redirecting to sign in…" />
      </div>
    );
  }

  return <AppShell>{children}</AppShell>;
}
