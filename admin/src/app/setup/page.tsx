"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Panel, PanelBody } from "@/components/ui/panel";
import { SelectControl } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { CodeBlock } from "@/components/ui/code";
import { Tag } from "@/components/ui/status-tag";
import { useApp } from "@/components/shell/app-context";
import {
  ApiError,
  apiGet,
  apiMutate,
  completeSetup,
  fetchConsoleStatus,
  fetchMe,
  type RawProviderEntry,
} from "@/lib/api";

type Phase = "checking" | "account" | "provider" | "merchant" | "done";

type ConfigureResult = {
  account: string | null;
  provider: string | null;
  merchant: string | null;
};

/**
 * First-install wizard, WordPress style: until the console has one account,
 * API/setup is open. Step 1 creates that owner (and signs the browser in);
 * the remaining ConfigureResult steps are one-time shortcuts for the normal
 * providers / merchants / api-keys screens. Every step after the first is
 * skippable — the ordinary console screens can do it all later.
 */
export default function SetupPage() {
  const router = useRouter();
  const { refreshAuth } = useApp();
  const [phase, setPhase] = useState<Phase>("checking");
  const [fatal, setFatal] = useState<string | null>(null);
  const [result, setResult] = useState<ConfigureResult>({
    account: null,
    provider: null,
    merchant: null,
  });

  // An already-signed-in visitor (or an installed console) leaves: /setup is
  // only for the very first minutes of an installation.
  useEffect(() => {
    fetchMe()
      .then(() => router.replace("/"))
      .catch(async () => {
        try {
          const status = await fetchConsoleStatus();
          if (status.data.needs_setup) {
            setPhase("account");
          } else {
            router.replace("/login");
          }
        } catch {
          setFatal("This console cannot reach its admin API yet.");
        }
      });
  }, [router]);

  if (phase === "checking") {
    return (
      <div className="flex min-h-dvh items-center justify-center p-6">
        <Panel className="w-full max-w-lg">
          <PanelBody className="flex flex-col gap-3 p-6">
            <h1 className="text-[15px] font-medium text-ink">
              AwsemPay admin console
            </h1>
            {fatal ? (
              <p className="text-[13px] text-failed">{fatal}</p>
            ) : (
              <p className="text-[13px] text-secondary">Checking install state…</p>
            )}
          </PanelBody>
        </Panel>
      </div>
    );
  }

  return (
    <div className="flex min-h-dvh items-start justify-center p-6">
      <div className="flex w-full max-w-2xl flex-col gap-4">
        <div className="pb-1">
          <h1 className="text-[15px] font-medium text-ink">
            AwsemPay admin setup
          </h1>
          <p className="mt-1 text-[13px] text-secondary">
            First install: create the console owner, then optionally wire the
            first gateway to the first merchant. Later steps are skippable —
            the console screens cover everything once you are signed in.
          </p>
        </div>

        <AccountStep
          phase={phase}
          account={result.account}
          onDone={(email) => {
            setResult((r) => ({ ...r, account: email }));
            setPhase("provider");
          }}
        />
        {phase === "provider" ? (
          <ProviderStep
            onDone={(configured) => {
              setResult((r) => ({ ...r, provider: configured }));
              setPhase("merchant");
            }}
          />
        ) : null}
        {phase === "merchant" ? (
          <MerchantStep
            onDone={(summary) => {
              setResult((r) => ({ ...r, merchant: summary }));
              setPhase("done");
              void refreshAuth();
            }}
          />
        ) : null}
        {phase === "done" ? <DoneStep result={result} /> : null}
      </div>
    </div>
  );
}

/* --- step 1: owner account ------------------------------------------------ */

function AccountStep({
  phase,
  account,
  onDone,
}: {
  phase: Phase;
  account: string | null;
  onDone: (email: string) => void;
}) {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const settled = phase !== "account";

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setFieldError({});
    try {
      const me = await completeSetup(name, email, password);
      onDone(me.data.user.email);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 400 && cause.field) {
        setFieldError({ [cause.field]: cause.message });
      } else if (cause instanceof ApiError && cause.status === 409) {
        setError("This console was set up in the meantime — sign in instead.");
      } else {
        setError(cause instanceof ApiError ? cause.message : "Setup failed — the admin API is unreachable.");
      }
      setBusy(false);
    }
  }

  /** A generated default: 16 random bytes as base64url (~22 characters). */
  function suggest() {
    const bytes = new Uint8Array(16);
    crypto.getRandomValues(bytes);
    setPassword(
      btoa(String.fromCharCode(...bytes))
        .replaceAll("+", "-")
        .replaceAll("/", "_")
        .replaceAll("=", ""),
    );
  }

  return (
    <Panel>
      <PanelBody className="flex flex-col gap-4 p-6">
        <div className="flex items-center gap-2">
          <Tag tone={settled ? "ok" : "accent"}>Step 1</Tag>
          <h2 className="text-[15px] font-medium text-ink">Owner account</h2>
        </div>
        {settled ? (
          <p className="text-[13px] text-secondary">
            Signed in as <span className="font-mono">{account ?? "the account you created"}</span>.
          </p>
        ) : (
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <p className="text-[13px] text-secondary">
              This is the first console account, role owner. It replaces
              running <code className="font-mono">openpay create-admin</code>{" "}
              by hand.
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Name" htmlFor="setup-name" required>
                <Input
                  id="setup-name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  autoComplete="name"
                  required
                />
              </Field>
              <Field
                label="Email"
                htmlFor="setup-email"
                required
                error={fieldError.email}
              >
                <Input
                  id="setup-email"
                  type="email"
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                  autoComplete="email"
                  required
                />
              </Field>
            </div>
            <Field
              label="Password"
              htmlFor="setup-password"
              required
              help="At least 10 characters. It is stored hashed; nothing can show it again."
              error={fieldError.password}
            >
              <div className="flex items-center gap-1.5">
                <Input
                  id="setup-password"
                  type="text"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  autoComplete="new-password"
                  spellCheck={false}
                  className="font-mono text-[12px]"
                  required
                />
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={suggest}
                >
                  Suggest
                </Button>
              </div>
            </Field>
            {error ? <p className="text-[13px] text-failed">{error}</p> : null}
            <Button type="submit" variant="primary" disabled={busy}>
              {busy ? "Creating account…" : "Create account and continue"}
            </Button>
          </form>
        )}
      </PanelBody>
    </Panel>
  );
}

/* --- step 2: first gateway ------------------------------------------------ */

function ProviderStep({ onDone }: { onDone: (configured: string | null) => void }) {
  const [adapters, setAdapters] = useState<RawProviderEntry[]>([]);
  const [selected, setSelected] = useState<RawProviderEntry | null>(null);
  const [env, setEnv] = useState("sandbox");
  const [values, setValues] = useState<Record<string, string>>({});
  const [fieldError, setFieldError] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState<string | null>(null);

  useEffect(() => {
    apiGet<RawProviderEntry[]>("/providers", { env: env })
      .then((response) => setAdapters(response.data))
      .catch(() => setAdapters([]))
      .finally(() => setLoading(false));
  }, [env]);

  function choose(name: string) {
    const adapter = adapters.find((a) => a.name === name) ?? null;
    setSelected(adapter);
    setValues({});
    setFieldError({});
    setSaved(null);
    if (adapter && !adapter.environments.includes(env)) {
      setEnv(adapter.environments[0]);
    }
  }

  async function save() {
    if (!selected) return;
    const next: Record<string, string> = {};
    for (const field of selected.config_fields) {
      const value = values[field.key]?.trim() ?? "";
      if (field.required && value === "") {
        next[field.key] = `${field.label} is required.`;
      }
    }
    setFieldError(next);
    if (Object.keys(next).length > 0) return;

    setBusy(true);
    setError(null);
    try {
      await apiMutate("PUT", `/providers/${selected.name}/config`, {
        environment: env,
        config: values,
      });
      setSaved(`${selected.label} configured for ${env}.`);
    } catch (cause) {
      setError(
        cause instanceof ApiError
          ? `The adapter rejected this configuration: ${cause.message}`
          : "Saving failed — the admin API is unreachable.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel>
      <PanelBody className="flex flex-col gap-4 p-6">
        <div className="flex items-center gap-2">
          <Tag tone={saved ? "ok" : "accent"}>Step 2</Tag>
          <h2 className="text-[15px] font-medium text-ink">First gateway</h2>
          {!saved ? (
            <Button
              size="sm"
              variant="ghost"
              className="ml-auto"
              onClick={() => onDone(null)}
            >
              Skip for now
            </Button>
          ) : null}
        </div>
        {saved ? (
          <>
            <p className="text-[13px] text-secondary">{saved}</p>
            <Button variant="primary" onClick={() => onDone(saved)}>
              Continue
            </Button>
          </>
        ) : loading ? (
          <p className="text-[13px] text-secondary">Loading provider catalog…</p>
        ) : (
          <>
            <p className="text-[13px] text-secondary">
              One gateway for the environment you will test in. Sandbox
              credentials never move real money; production can wait.
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Gateway" htmlFor="setup-provider">
                <SelectControl
                  id="setup-provider"
                  ariaLabel="Gateway adapter"
                  value={selected?.name ?? ""}
                  onValueChange={choose}
                  options={[
                    { value: "", label: "Select a gateway" },
                    ...adapters.map((a) => ({ value: a.name, label: a.label })),
                  ]}
                  className="w-full"
                />
              </Field>
              <Field
                label="Environment"
                help={
                  selected && selected.environments.length === 1
                    ? `${selected.label} is sandbox-only.`
                    : undefined
                }
              >
                <SelectControl
                  ariaLabel="Credentials environment"
                  value={env}
                  onValueChange={setEnv}
                  options={[
                    { value: "sandbox", label: "Sandbox" },
                    { value: "production", label: "Production" },
                  ]}
                  className="w-full"
                />
              </Field>
            </div>
            {selected ? (
              <>
                <p className="text-[12px] text-muted">{selected.summary}</p>
                {selected.config_fields.map((field) => (
                  <Field
                    key={field.key}
                    label={field.label}
                    htmlFor={`setup-${field.key}`}
                    required={field.required}
                    help={field.help}
                    error={fieldError[field.key]}
                  >
                    {field.multiline ? (
                      <Textarea
                        id={`setup-${field.key}`}
                        value={values[field.key] ?? ""}
                        onChange={(event) =>
                          setValues((v) => ({ ...v, [field.key]: event.target.value }))
                        }
                        placeholder={field.placeholder}
                        autoComplete="off"
                        spellCheck={false}
                        className="font-mono text-[11.5px]"
                      />
                    ) : field.options ? (
                      <SelectControl
                        ariaLabel={field.label}
                        value={values[field.key] || undefined}
                        onValueChange={(value) =>
                          setValues((v) => ({ ...v, [field.key]: value }))
                        }
                        options={[
                          { value: "", label: "Default" },
                          ...field.options.map((option) => ({
                            value: option,
                            label: option,
                          })),
                        ]}
                        className="w-full"
                      />
                    ) : (
                      <Input
                        id={`setup-${field.key}`}
                        type={field.secret ? "password" : "text"}
                        value={values[field.key] ?? ""}
                        onChange={(event) =>
                          setValues((v) => ({ ...v, [field.key]: event.target.value }))
                        }
                        placeholder={field.placeholder}
                        autoComplete="off"
                      />
                    )}
                  </Field>
                ))}
                {error ? <p className="text-[13px] text-failed">{error}</p> : null}
                <Button variant="primary" disabled={busy} onClick={() => void save()}>
                  {busy ? "Validating and saving…" : "Save credentials"}
                </Button>
              </>
            ) : null}
          </>
        )}
      </PanelBody>
    </Panel>
  );
}

/* --- step 3: first merchant + API key ------------------------------------- */

function MerchantStep({ onDone }: { onDone: (summary: string | null) => void }) {
  const [name, setName] = useState("");
  const [callbackURL, setCallbackURL] = useState("");
  const [env, setEnv] = useState("sandbox");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [webhookSecret, setWebhookSecret] = useState<string | null>(null);
  const [apiKey, setApiKey] = useState<string | null>(null);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const created = await apiMutate<{ id: string }>("POST", "/merchants", {
        name,
        callback_url: callbackURL,
      });
      const secret =
        typeof created.meta.webhook_secret === "string"
          ? created.meta.webhook_secret
          : null;
      setWebhookSecret(secret);

      const key = await apiMutate<{ prefix: string }>("POST", "/api-keys", {
        merchant_id: created.data.id,
        label: "Initial key",
        environment: env,
      });
      setApiKey(typeof key.meta.key === "string" ? key.meta.key : null);
      setBusy(false);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : "The request failed — check the fields and try again.");
      setBusy(false);
    }
  }

  return (
    <Panel>
      <PanelBody className="flex flex-col gap-4 p-6">
        <div className="flex items-center gap-2">
          <Tag tone={apiKey ? "ok" : "accent"}>Step 3</Tag>
          <h2 className="text-[15px] font-medium text-ink">
            First merchant and API key
          </h2>
          {!apiKey ? (
            <Button
              size="sm"
              variant="ghost"
              className="ml-auto"
              onClick={() => onDone(null)}
            >
              Skip for now
            </Button>
          ) : null}
        </div>
        {apiKey ? (
          <>
            <p className="text-[13px] text-secondary">
              Both secrets show exactly once — store them now.
            </p>
            {webhookSecret ? (
              <CodeBlock label="webhook_secret" code={webhookSecret} />
            ) : null}
            <CodeBlock label="api_key" code={apiKey} />
            <Button
              variant="primary"
              onClick={() => onDone(`${name}, key ${env}`)}
            >
              Continue
            </Button>
          </>
        ) : (
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <p className="text-[13px] text-secondary">
              One merchant to try the platform with. Merchants you add later
              get their keys from the console&apos;s API-keys screen.
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Merchant name" htmlFor="setup-merchant-name" required>
                <Input
                  id="setup-merchant-name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  required
                />
              </Field>
              <Field
                label="Callback URL"
                htmlFor="setup-merchant-callback"
                help="Where payment events are signed and sent."
              >
                <Input
                  id="setup-merchant-callback"
                  type="url"
                  value={callbackURL}
                  onChange={(event) => setCallbackURL(event.target.value)}
                  placeholder="https://"
                />
              </Field>
            </div>
            <Field
              label="Key environment"
              help="sk_test_ keys can only reach the sandbox."
            >
              <SelectControl
                ariaLabel="Key environment"
                value={env}
                onValueChange={setEnv}
                options={[
                  { value: "sandbox", label: "Sandbox" },
                  { value: "production", label: "Production" },
                ]}
                className="w-full"
              />
            </Field>
            {error ? <p className="text-[13px] text-failed">{error}</p> : null}
            <Button type="submit" variant="primary" disabled={busy}>
              {busy ? "Creating merchant…" : "Create merchant and key"}
            </Button>
          </form>
        )}
      </PanelBody>
    </Panel>
  );
}

/* --- final ----------------------------------------------------------------- */

function DoneStep({ result }: { result: ConfigureResult }) {
  const router = useRouter();
  const rows = [
    { label: "Owner account", value: result.account ?? "created" },
    { label: "First gateway", value: result.provider ?? "skipped" },
    { label: "First merchant", value: result.merchant ?? "skipped" },
  ];
  return (
    <Panel>
      <PanelBody className="flex flex-col gap-4 p-6">
        <div className="flex items-center gap-2">
          <Tag tone="ok">Done</Tag>
          <h2 className="text-[15px] font-medium text-ink">
            The console is ready
          </h2>
        </div>
        <dl className="flex flex-col gap-1.5">
          {rows.map((row) => (
            <div key={row.label} className="flex gap-2 text-[13px]">
              <dt className="w-32 shrink-0 text-muted">{row.label}</dt>
              <dd className="text-secondary">{row.value}</dd>
            </div>
          ))}
        </dl>
        <p className="text-[12px] text-muted">
          Gateway credentials, merchants, keys and routing all live on the
          console. Sign-outs and new accounts work from Users; the provider
          list shows whether each gateway instance is live.
        </p>
        <Button variant="primary" onClick={() => router.push("/")}>
          Open dashboard
        </Button>
      </PanelBody>
    </Panel>
  );
}
