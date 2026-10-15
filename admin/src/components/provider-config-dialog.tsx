"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { SelectControl } from "@/components/ui/select";
import { DialogContent, DialogRoot } from "@/components/ui/dialog";
import type { Environment, Provider } from "@/lib/demo-data";

type Props = {
  provider: Provider;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  environment: Environment;
  scopeLabel: string;
  allowMerchantScope?: boolean;
  allowEnvironmentSelect?: boolean;
  onSaved: (summary: string) => void;
};

export function ProviderConfigDialog({
  provider,
  open,
  onOpenChange,
  environment,
  scopeLabel,
  allowMerchantScope = false,
  allowEnvironmentSelect = false,
  onSaved,
}: Props) {
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      provider.configFields.map((field) => [field.key, field.value ?? ""]),
    ),
  );
  const [scope, setScope] = useState(allowMerchantScope ? "merchant" : "platform");
  const [env, setEnv] = useState<Environment>(environment);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [validated, setValidated] = useState<string | null>(null);

  const alreadyConfigured = provider.state === "configured";

  function setValue(key: string, value: string) {
    setValues((current) => ({ ...current, [key]: value }));
  }

  function validate(): boolean {
    const next: Record<string, string> = {};
    for (const field of provider.configFields) {
      const value = values[field.key]?.trim() ?? "";
      const keptSecret = field.secret && alreadyConfigured && value === "";
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

  return (
    <DialogRoot
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        if (!next) {
          setErrors({});
          setValidated(null);
        }
      }}
    >
      <DialogContent
        title={`Configure ${provider.label}`}
        description={`${scopeLabel} / ${env}. Values are stored server side and never returned in full.`}
        footer={
          <>
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              variant="secondary"
              onClick={() => {
                if (validate()) {
                  setValidated(
                    "All required fields are present and URLs are well formed. Live credential checks run server side.",
                  );
                } else {
                  setValidated(null);
                }
              }}
            >
              Validate fields
            </Button>
            <Button
              variant="primary"
              onClick={() => {
                if (!validate()) return;
                onSaved(`${provider.label} credentials saved for ${scopeLabel} / ${env}.`);
                onOpenChange(false);
              }}
            >
              Save credentials
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4">
          {allowMerchantScope ? (
            <Field
              label="Scope"
              help="Platform defaults apply everywhere until a merchant overrides them."
            >
              <SelectControl
                ariaLabel="Credential scope"
                value={scope}
                onValueChange={setScope}
                options={[
                  { value: "platform", label: "Platform default" },
                  { value: "merchant", label: "Per merchant override" },
                ]}
                className="w-full"
              />
            </Field>
          ) : null}

          {allowMerchantScope && scope === "merchant" ? (
            <Field label="Merchant" help="Overrides are stored against this merchant only.">
              <SelectControl
                ariaLabel="Merchant"
                value="demo-a"
                onValueChange={() => undefined}
                options={[{ value: "demo-a", label: "Demo Merchant A" }]}
                className="w-full"
              />
            </Field>
          ) : null}

          {allowEnvironmentSelect ? (
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
          ) : null}

          {provider.configFields.map((field) => {
            const keptSecret = field.secret && alreadyConfigured;
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

          {validated ? (
            <p className="rounded-panel border border-border bg-surface-muted px-3 py-2 text-[12px] text-secondary">
              {validated}
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
