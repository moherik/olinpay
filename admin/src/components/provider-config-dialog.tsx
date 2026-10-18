"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { SelectControl } from "@/components/ui/select";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import { apiMutate, ApiError } from "@/lib/api";
import type { Environment, Provider } from "@/lib/demo-data";

type Props = {
  provider: Provider;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  environment: Environment;
  /** Whether at least one secret is stored for this environment. */
  configured: boolean;
  /** Owner role required: the backend rejects everyone else. */
  canManage: boolean;
  onSaved: (summary: string) => void;
  onChanged: () => void;
};

/**
 * Credentials are global per provider + environment (not per merchant): the
 * platform aims one gateway account at each environment. Secret fields are
 * write-only — the backend never echoes them, so a blank secret keeps the
 * stored value.
 */
export function ProviderConfigDialog({
  provider,
  open,
  onOpenChange,
  environment,
  configured,
  canManage,
  onSaved,
  onChanged,
}: Props) {
  // Initial values are set on mount; the parent keys this component per
  // provider, so a fresh mount always starts with fresh state.
  const [env, setEnv] = useState<Environment>(environment);
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      provider.configFields.map((field) => [field.key, field.value ?? ""]),
    ),
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<string | null>(null);
  const [feedbackTone, setFeedbackTone] = useState<"ok" | "danger">("ok");

  function setValue(key: string, value: string) {
    setValues((current) => ({ ...current, [key]: value }));
  }

  function validate(): boolean {
    const next: Record<string, string> = {};
    for (const field of provider.configFields) {
      const value = values[field.key]?.trim() ?? "";
      const keptSecret = field.secret && configured && value === "";
      if (field.required && value === "" && !keptSecret) {
        next[field.key] = `${field.label} is required.`;
        continue;
      }
      if (field.type === "url" && value !== "" && !value.startsWith("https://")) {
        next[field.key] = "Use an https:// URL.";
      }
    }
    setErrors(next);
    return Object.keys(next).length === 0;
  }

  async function save() {
    if (!canManage) return;
    if (!validate()) return;
    setBusy(true);
    setFeedback(null);
    try {
      await apiMutate("PUT", `/providers/${provider.name}/config`, {
        environment: env,
        config: values,
      });
      onSaved(`${provider.label} credentials saved for ${env}.`);
      onOpenChange(false);
      onChanged();
    } catch (cause) {
      setFeedbackTone("danger");
      setFeedback(
        cause instanceof ApiError
          ? `The adapter rejected this configuration: ${cause.message}`
          : "Saving failed — the admin API is unreachable.",
      );
    } finally {
      setBusy(false);
    }
  }

  const alreadyConfigured = configured;

  return (
    <DialogRoot
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (!next) {
          setErrors({});
          setFeedback(null);
        }
      }}
    >
      <DialogContent
        title={`Configure ${provider.label}`}
        description={`Platform credentials for ${env}. The adapter validates them on save; secrets are never returned in full.`}
        footer={
          <>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              disabled={busy || !canManage}
              onClick={() => void save()}
            >
              {busy ? "Validating and saving…" : "Save credentials"}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4">
          {!canManage ? (
            <p className="text-[12px] text-pending">
              Saving credentials requires the owner role.
            </p>
          ) : null}

          <Field
            label="Environment"
            help="Sandbox and production are separate credentials. A sandbox key cannot move money."
          >
            <SelectControl
              ariaLabel="Credentials environment"
              value={env}
              onValueChange={(value) => setEnv(value as Environment)}
              options={[
                { value: "sandbox", label: "Sandbox" },
                { value: "production", label: "Production" },
              ]}
              className="w-full"
            />
          </Field>

          {provider.configFields.map((field) => {
            const keptSecret = field.secret && configured;
            return (
              <Field
                key={field.key}
                label={field.label}
                htmlFor={`cfg-${provider.name}-${field.key}`}
                required={field.required}
                help={
                  keptSecret
                    ? "A value is already stored. Leave this blank to keep it."
                    : field.help
                }
                error={errors[field.key]}
              >
                {field.type === "select" ? (
                  <SelectControl
                    ariaLabel={field.label}
                    value={values[field.key] || undefined}
                    onValueChange={(value) => setValue(field.key, value)}
                    options={(field.options ?? []).map((option) => ({
                      value: option,
                      label: option,
                    }))}
                    className="w-full"
                  />
                ) : (
                  <Input
                    id={`cfg-${provider.name}-${field.key}`}
                    type={field.type === "password" ? "password" : "text"}
                    value={values[field.key] ?? ""}
                    onChange={(event) => setValue(field.key, event.target.value)}
                    placeholder={
                      keptSecret ? "Stored, leave blank to keep" : undefined
                    }
                    autoComplete="off"
                  />
                )}
              </Field>
            );
          })}

          {alreadyConfigured ? (
            <p className="text-[12px] text-muted">
              Saved credentials stay stored until removed. Removing them from
              this environment retires the live gateway instance.
            </p>
          ) : null}

          {feedback ? (
            <p
              className={`rounded-panel border border-border bg-surface-muted px-3 py-2 text-[12px] ${
                feedbackTone === "ok" ? "text-secondary" : "text-failed"
              }`}
            >
              {feedback}
            </p>
          ) : null}

          {provider.note ? (
            <p className="text-[12px] text-muted">{provider.note}</p>
          ) : null}
        </div>
      </DialogContent>
    </DialogRoot>
  );
}
