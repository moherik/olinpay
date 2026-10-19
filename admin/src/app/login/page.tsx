"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelBody } from "@/components/ui/panel";
import { useApp } from "@/components/shell/app-context";
import { ApiError, apiMutate, fetchConsoleStatus, fetchMe } from "@/lib/api";

/**
 * The only sign-in screen. Credentials verify against the backend's
 * bcrypt store; the API sets the HttpOnly session cookie on the response.
 * A console with no account yet never shows this form — the visitor is
 * sent straight to the first-install setup wizard.
 */
export default function LoginPage() {
  const router = useRouter();
  const { auth, refreshAuth } = useApp();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [toSetup, setToSetup] = useState(false);

  // An already-authenticated visitor skips the form entirely.
  useEffect(() => {
    fetchMe()
      .then(() => router.replace("/"))
      .catch(async () => {
        // On a console with no account at all, the wizard is the way in.
        try {
          const status = await fetchConsoleStatus();
          if (status.data.needs_setup) {
            setToSetup(true);
            router.replace("/setup");
          }
        } catch {
          /* the API is down; the form still renders with its own error */
        }
      });
  }, [router, auth]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await apiMutate("POST", "/login", { email, password });
      await refreshAuth();
      router.replace("/");
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) {
        setError("Email and password do not match an active account.");
      } else if (cause instanceof ApiError && cause.status === 403) {
        setError("This account is suspended.");
      } else {
        setError("Sign-in failed — cannot reach the admin API.");
      }
      setBusy(false);
    }
  }

  if (toSetup) {
    return (
      <div className="flex min-h-dvh items-center justify-center p-6">
        <Panel className="w-full max-w-sm">
          <PanelBody className="p-6">
            <p className="text-[13px] text-secondary">
              This console has no account yet — opening the first-install
              setup…
            </p>
          </PanelBody>
        </Panel>
      </div>
    );
  }

  return (
    <div className="flex min-h-dvh items-center justify-center p-6">
      <Panel className="w-full max-w-sm">
        <PanelBody className="flex flex-col gap-4 p-6">
          <div>
            <h1 className="text-[15px] font-medium text-ink">
              AwsemPay admin console
            </h1>
            <p className="mt-1 text-[13px] text-secondary">
              Sign in with your console account. Sessions expire after 12 hours.
            </p>
          </div>
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <Field
              label="Email"
              htmlFor="login-email"
            >
              <Input
                id="login-email"
                type="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                autoComplete="username"
                required
              />
            </Field>
            <Field label="Password" htmlFor="login-password">
              <Input
                id="login-password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete="current-password"
                required
              />
            </Field>
            {error ? <p className="text-[13px] text-failed">{error}</p> : null}
            <Button type="submit" variant="primary" disabled={busy}>
              {busy ? "Signing in…" : "Sign in"}
            </Button>
          </form>
        </PanelBody>
      </Panel>
    </div>
  );
}
