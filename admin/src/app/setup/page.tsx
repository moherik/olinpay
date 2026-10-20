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
import {
  continueWithoutKey,
  initialState,
  keyCreated,
  keyFailed,
  merchantCreated,
} from "@/lib/setup-state.mjs";
import { cn } from "@/lib/utils";

const ORDER = ["account", "provider", "merchant", "done"] as const;
type StepKey = (typeof ORDER)[number];
type MerchantSetupState =
  | typeof initialState
  | ReturnType<typeof merchantCreated>
  | ReturnType<typeof keyFailed>
  | ReturnType<typeof keyCreated>
  | ReturnType<typeof continueWithoutKey>;

/**
 * What a wizard slot holds:
 * - null  — untouched, not reached yet
 * - "skipped" — the visitor moved past it; the slot stays visible and openable
 * - a string — done, and this is the one-line summary to show afterwards
 */
type Fill = null | "skipped" | string;

type StepState = "active" | "complete" | "skipped" | "pending";

const stateLabels: Record<StepState, string> = {
  active: "In progress",
  complete: "Done",
  skipped: "Skipped",
  pending: "Waiting",
};

const tagTones: Record<StepState, "accent" | "ok" | "neutral"> = {
  active: "accent",
  complete: "ok",
  skipped: "neutral",
  pending: "neutral",
};

/**
 * First-install wizard, WordPress style: until the console has one account,
 * API/setup is open. Step 1 creates that owner (and signs the browser in);
 * the remaining slots are one-time shortcuts for the normal providers /
 * merchants / api-keys screens. Every slot after the first is skippable, and
 * nothing ever disappears: skipped slots stay on the page and any earlier
 * slot can be reopened before finishing — Firebase create-project style.
 */
export default function SetupPage() {
  const router = useRouter();
  const { refreshAuth } = useApp();
  const [active, setActive] = useState<StepKey>("account");
  const [fatal, setFatal] = useState<string | null>(null);
  const [account, setAccount] = useState<string | null>(null);
  const [provider, setProvider] = useState<Fill>(null);
  const [merchant, setMerchant] = useState<Fill>(null);
  const [merchantSetup, setMerchantSetup] = useState<MerchantSetupState>(
    initialState,
  );

  // An already-signed-in visitor (or an installed console) leaves: /setup is
  // only for the very first minutes of an installation.
  useEffect(() => {
    fetchMe()
      .then(() => router.replace("/"))
      .catch(async () => {
        try {
          const status = await fetchConsoleStatus();
          if (status.data.needs_setup) {
            setActive("account");
          } else {
            router.replace("/login");
          }
        } catch {
          setFatal("This console cannot reach its admin API yet.");
        }
      });
  }, [router]);

  function status(slot: StepKey): StepState {
    if (slot === active) return "active";
    if (slot === "account") return account ? "complete" : "pending";
    if (slot === "provider") return fillState(provider);
    if (slot === "merchant") return fillState(merchant);
    // The final summary slot lights up once every earlier slot is settled.
    return account !== null && provider !== null && merchant !== null
      ? "complete"
      : "pending";
  }

  function fillState(fill: Fill): StepState {
    if (fill === null) return "pending";
    return fill === "skipped" ? "skipped" : "complete";
  }

  /** After settling slot, move focus to the next one. */
  function advanceFrom(slot: StepKey) {
    const next = ORDER[ORDER.indexOf(slot) + 1];
    if (next) setActive(next);
  }

  if (fatal) {
    return (
      <div className="flex min-h-dvh items-center justify-center p-6">
        <Panel className="w-full max-w-lg">
          <PanelBody className="flex flex-col gap-3 p-6">
            <h1 className="text-[15px] font-medium text-ink">
              AwsemPay admin console
            </h1>
            <p className="text-[13px] text-failed">{fatal}</p>
          </PanelBody>
        </Panel>
      </div>
    );
  }

  return (
    <div className="flex min-h-dvh items-start justify-center p-6 [&_button]:min-h-11 sm:[&_button]:min-h-0">
      <div className="flex w-full max-w-2xl flex-col gap-4">
        <div className="pb-1">
          <h1 className="text-[15px] font-medium text-ink">
            AwsemPay admin setup
          </h1>
          <p className="mt-1 text-[13px] text-secondary">
            First install: create the console owner, then optionally wire the
            first gateway to the first merchant. Skipped steps stay on this
            page and can be revisited before you finish.
          </p>
        </div>

        <StepCard
          index={1}
          title="Owner account"
          state={status("account")}
          hint={
            account
              ? `Signed in as ${account}.`
              : "The first console account, role owner."
          }
          clickable={account !== null}
          onOpen={() => setActive("account")}
        >
          {account ? (
            <Button
              variant="primary"
              onClick={() => advanceFrom("account")}
            >
              Continue
            </Button>
          ) : (
            <AccountForm
              onDone={(email) => {
                setAccount(email);
                void refreshAuth();
                advanceFrom("account");
              }}
            />
          )}
        </StepCard>

        <StepCard
          index={2}
          title="First gateway"
          state={status("provider")}
          hint={
            provider === null
              ? "Optionally configure one gateway for the environment you will test in. Skippable."
              : provider === "skipped"
                ? "Skipped — no gateway configured yet. You can still do this here."
                : provider
          }
          clickable={provider !== null}
          onOpen={() => setActive("provider")}
        >
          <ProviderForm
            fill={provider}
            onBack={() => setActive("account")}
            onDone={(value) => {
              setProvider(value);
              advanceFrom("provider");
            }}
          />
        </StepCard>

        <StepCard
          index={3}
          title="First merchant and API key"
          state={status("merchant")}
          hint={
            merchant === null
              ? "One merchant to try the platform with. Skippable."
              : merchant === "skipped"
                ? "Skipped — you can still create the first merchant here."
                : merchant
          }
          clickable={merchant !== null || merchantSetup.status !== "initial"}
          onOpen={() => setActive("merchant")}
        >
          <MerchantForm
            fill={merchant}
            setupState={merchantSetup}
            onBack={() => setActive("provider")}
            onSetupStateChange={setMerchantSetup}
            onDone={(value) => {
              setMerchant(value);
              advanceFrom("merchant");
            }}
          />
        </StepCard>

        <StepCard
          index={4}
          title="The console is ready"
          state={status("done")}
          hint={
            status("done") === "complete"
              ? "Review the summary and open the dashboard."
              : "The summary appears here once the steps above are settled."
          }
          clickable={status("done") === "complete"}
          onOpen={() => setActive("done")}
        >
          <DoneStep
            result={{ account, provider, merchant }}
            onBack={() => setActive("merchant")}
          />
        </StepCard>
      </div>
    </div>
  );
}

/* --- step card shell ------------------------------------------------------- */

function StepCard({
  index,
  title,
  state,
  hint,
  clickable,
  onOpen,
  children,
}: {
  index: number;
  title: string;
  state: StepState;
  /** The one line shown while the slot is collapsed. */
  hint: string;
  /** Reopening a settled slot from its header. The open slot cannot be open. */
  clickable: boolean;
  onOpen: () => void;
  children: React.ReactNode;
}) {
  return (
    <Panel>
      <PanelBody className="flex flex-col gap-4 p-6">
        <button
          type="button"
          onClick={onOpen}
          disabled={!clickable || state === "active"}
          aria-expanded={state === "active"}
          className={cn(
            "flex min-h-11 items-center gap-2 text-left sm:min-h-0",
            clickable && state !== "active"
              ? "cursor-pointer"
              : "cursor-default",
          )}
        >
          <Tag tone={tagTones[state]}>Step {index}</Tag>
          <h2 className="text-[15px] font-medium text-ink">{title}</h2>
          <span className="ml-auto text-[12px] text-muted">
            {stateLabels[state]}
          </span>
        </button>
        {state === "active" ? (
          children
        ) : (
          <p className="text-[13px] text-secondary">{hint}</p>
        )}
      </PanelBody>
    </Panel>
  );
}

/** A generated default: 16 random bytes as base64url (~22 characters). */
function suggestPassword() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return btoa(String.fromCharCode(...bytes))
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
}

/* --- step 1 form: owner account -------------------------------------------- */

function AccountForm({ onDone }: { onDone: (email: string) => void }) {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

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
        setError(
          cause instanceof ApiError
            ? cause.message
            : "Setup failed — the admin API is unreachable.",
        );
      }
      setBusy(false);
    }
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={submit}>
      <p className="text-[13px] text-secondary">
        This creates the first console account with the owner role.
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
            onClick={() => setPassword(suggestPassword())}
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
  );
}

/* --- step 2 form: first gateway --------------------------------------------- */

function ProviderForm({
  fill,
  onBack,
  onDone,
}: {
  fill: Fill;
  onBack: () => void;
  onDone: (value: Fill) => void;
}) {
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

  if (fill !== null && fill !== "skipped") {
    // Completed earlier and reopened: credentials live on the provider, only
    // the outcome line is shown; editing reopens the catalog below.
    return (
      <div className="flex flex-col gap-4">
        <p className="text-[13px] text-secondary">{fill}</p>
        {saved === null ? (
          <div className="flex items-center gap-2">
            <Button variant="ghost" onClick={onBack}>
              Back
            </Button>
            <Button
              variant="primary"
              className="ml-auto"
              onClick={() => onDone(fill)}
            >
              Continue
            </Button>
          </div>
        ) : null}
      </div>
    );
  }

  if (saved !== null) {
    return (
      <div className="flex flex-col gap-4">
        <p className="text-[13px] text-secondary">{saved}</p>
        <div className="flex items-center gap-2">
          <Button variant="ghost" onClick={onBack}>
            Back
          </Button>
          <div className="ml-auto flex items-center gap-2">
            <Button variant="ghost" onClick={() => setSaved(null)}>
              Edit credentials
            </Button>
            <Button variant="primary" onClick={() => onDone(saved)}>
              Continue
            </Button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <p className="text-[13px] text-secondary">
        One gateway for the environment you will test in. Sandbox credentials
        never move real money; production can wait.
      </p>
      {loading ? (
        <p className="text-[13px] text-secondary">Loading provider catalog…</p>
      ) : (
        <>
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
            </>
          ) : null}
          <div className="flex items-center gap-2">
            <Button variant="ghost" onClick={onBack}>
              Back
            </Button>
            {selected ? (
              <Button
                variant="primary"
                className="ml-auto"
                disabled={busy}
                onClick={() => void save()}
              >
                {busy ? "Validating and saving…" : "Save credentials"}
              </Button>
            ) : (
              <Button
                variant="ghost"
                className="ml-auto"
                onClick={() => onDone("skipped")}
              >
                Skip for now
              </Button>
            )}
          </div>
        </>
      )}
    </div>
  );
}

/* --- step 3 form: first merchant + API key ----------------------------------- */

function MerchantForm({
  fill,
  setupState,
  onBack,
  onSetupStateChange,
  onDone,
}: {
  fill: Fill;
  setupState: MerchantSetupState;
  onBack: () => void;
  onSetupStateChange: (state: MerchantSetupState) => void;
  onDone: (value: Fill) => void;
}) {
  const [name, setName] = useState("");
  const [callbackURL, setCallbackURL] = useState("");
  const [env, setEnv] = useState("sandbox");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const secretsVisible =
    setupState.status === "key-failed" ||
    setupState.status === "complete-with-key";

  useEffect(() => {
    if (!secretsVisible || fill !== null) return;

    const warnBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warnBeforeUnload);
    return () => window.removeEventListener("beforeunload", warnBeforeUnload);
  }, [fill, secretsVisible]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (setupState.status !== "initial") return;
    setBusy(true);
    setError(null);

    let merchantState: ReturnType<typeof merchantCreated>;
    try {
      const created = await apiMutate<{ id: string }>("POST", "/merchants", {
        name,
        callback_url: callbackURL,
      });
      merchantState = merchantCreated(initialState, {
        id: created.data.id,
        name,
        environment: env,
        webhookSecret:
          typeof created.meta.webhook_secret === "string"
            ? created.meta.webhook_secret
            : null,
      });
      onSetupStateChange(merchantState);
    } catch (cause) {
      setError(
        cause instanceof ApiError
          ? cause.message
          : "The request failed — check the fields and try again.",
      );
      setBusy(false);
      return;
    }

    try {
      const key = await apiMutate<{ prefix: string }>("POST", "/api-keys", {
        merchant_id: merchantState.merchant.id,
        label: "Initial key",
        environment: env,
      });
      if (typeof key.meta.key !== "string" || key.meta.key.trim() === "") {
        throw new TypeError("The API key response could not be confirmed.");
      }
      onSetupStateChange(keyCreated(merchantState, key.meta.key));
    } catch (cause) {
      onSetupStateChange(
        keyFailed(
          merchantState,
          cause instanceof ApiError
            ? cause.message
            : "The API key response could not be confirmed.",
        ),
      );
    } finally {
      setBusy(false);
    }
  }

  if (fill !== null && fill !== "skipped") {
    // Completed earlier and reopened. The summary only; more merchants and
    // keys belong to the console screens.
    return (
      <div className="flex flex-col gap-4">
        <p className="text-[13px] text-secondary">{fill}</p>
        <div className="flex items-center gap-2">
          <Button variant="ghost" onClick={onBack}>
            Back
          </Button>
          <Button
            variant="primary"
            className="ml-auto"
            onClick={() => onDone(fill)}
          >
            Continue
          </Button>
        </div>
      </div>
    );
  }

  if (setupState.status === "complete-with-key") {
    return (
      <div className="flex flex-col gap-4">
        <p className="text-[13px] text-secondary">
          Both secrets show exactly once. Store them now.
        </p>
        {setupState.merchant.webhookSecret ? (
          <CodeBlock
            label="webhook_secret"
            code={setupState.merchant.webhookSecret}
          />
        ) : null}
        <CodeBlock label="api_key" code={setupState.apiKey} />
        <div className="flex items-center gap-2">
          <Button variant="ghost" onClick={onBack}>
            Back
          </Button>
          <Button
            variant="primary"
            className="ml-auto"
            onClick={() =>
              onDone(`${setupState.merchant.name}, key ${setupState.merchant.environment}`)
            }
          >
            Continue
          </Button>
        </div>
      </div>
    );
  }

  if (setupState.status === "key-failed") {
    return (
      <div className="flex flex-col gap-4">
        <p className="text-[13px] text-failed" role="alert">
          The API key could not be confirmed: {setupState.keyError}
        </p>
        <p className="text-[13px] text-secondary">
          Your merchant was created. Finish setup, then use API Keys in the
          console to inspect or create keys manually. No additional key request
          was made.
        </p>
        {setupState.merchant.webhookSecret ? (
          <CodeBlock
            label="webhook_secret"
            code={setupState.merchant.webhookSecret}
          />
        ) : (
          <p className="text-[13px] text-secondary">
            The merchant response did not include a webhook secret.
          </p>
        )}
        <div className="flex items-center gap-2">
          <Button variant="ghost" onClick={onBack}>
            Back
          </Button>
          <Button
            variant="primary"
            className="ml-auto"
            onClick={() => {
              const complete = continueWithoutKey(setupState);
              onSetupStateChange(complete);
              onDone(`${complete.merchant.name}, no initial key`);
            }}
          >
            Continue without a key
          </Button>
        </div>
      </div>
    );
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={submit}>
      <p className="text-[13px] text-secondary">
        One merchant to try the platform with. Merchants you add later get
        their keys from the console&apos;s API-keys screen.
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
      <div className="flex items-center gap-2">
        <Button variant="ghost" onClick={onBack}>
          Back
        </Button>
        <div className="ml-auto flex items-center gap-2">
          <Button variant="ghost" onClick={() => onDone("skipped")}>
            Skip for now
          </Button>
          <Button type="submit" variant="primary" disabled={busy}>
            {busy ? "Creating merchant…" : "Create merchant and key"}
          </Button>
        </div>
      </div>
    </form>
  );
}

/* --- final summary ------------------------------------------------------------ */

function DoneStep({
  result,
  onBack,
}: {
  result: { account: string | null; provider: Fill; merchant: Fill };
  onBack: () => void;
}) {
  const router = useRouter();
  const rows = [
    {
      label: "Owner account",
      value: result.account ?? "created",
    },
    {
      label: "First gateway",
      value:
        result.provider === null
          ? "waiting"
          : result.provider === "skipped"
            ? "skipped"
            : result.provider,
    },
    {
      label: "First merchant",
      value:
        result.merchant === null
          ? "waiting"
          : result.merchant === "skipped"
            ? "skipped"
            : result.merchant,
    },
  ];
  return (
    <div className="flex flex-col gap-4">
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
        console. Sign-outs and new accounts work from Users; the provider list
        shows whether each gateway instance is live.
      </p>
      <div className="flex items-center gap-2">
        <Button variant="ghost" onClick={onBack}>
          Back
        </Button>
        <Button
          variant="primary"
          className="ml-auto"
          onClick={() => router.push("/")}
        >
          Open dashboard
        </Button>
      </div>
    </div>
  );
}
