"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, useState } from "react";
import {
  IconAudit,
  IconChevronDown,
  IconMerchants,
  IconOverview,
  IconProviders,
  IconRouting,
  IconTransactions,
  IconUsers,
  IconWebhooks,
} from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  DialogContent,
  DialogRoot,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownContent,
  DropdownItem,
  DropdownLabel,
  DropdownRadioGroup,
  DropdownRadioItem,
  DropdownRoot,
  DropdownSeparator,
  DropdownTrigger,
} from "@/components/ui/dropdown-menu";
import { useApp } from "@/components/shell/app-context";
import type { Environment } from "@/lib/demo-data";
import type { ThemeMode } from "@/lib/theme";
import { cn } from "@/lib/utils";

const navGroups = [
  {
    label: "Monitor",
    items: [
      { href: "/", label: "Overview", icon: IconOverview },
      { href: "/transactions", label: "Transactions", icon: IconTransactions },
      { href: "/webhooks", label: "Webhooks", icon: IconWebhooks },
    ],
  },
  {
    label: "Configure",
    items: [
      { href: "/merchants", label: "Merchants", icon: IconMerchants },
      { href: "/routing", label: "Routing", icon: IconRouting },
      { href: "/providers", label: "Providers", icon: IconProviders },
      { href: "/users", label: "Users and roles", icon: IconUsers },
    ],
  },
  {
    label: "Security",
    items: [
      { href: "/api-keys", label: "API keys", icon: IconProviders },
      { href: "/audit", label: "Audit log", icon: IconAudit },
    ],
  },
];

function isActive(pathname: string, href: string) {
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(`${href}/`);
}

/* Static stand-in while the path is not yet known. It carries no active state. */
function NavFallback() {
  return (
    <nav className="flex flex-col gap-4" aria-hidden="true">
      {navGroups.map((group) => (
        <div key={group.label}>
          <p className="px-2.5 pb-1 text-[11px] font-medium tracking-[0.02em] text-muted">
            {group.label}
          </p>
          <ul className="flex flex-col gap-0.5">
            {group.items.map((item) => (
              <li
                key={item.href}
                className="flex h-8 items-center gap-2 rounded-[3px] px-2.5 text-[13px] text-muted"
              >
                <item.icon size={16} />
                {item.label}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

function NavLinks({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  return (
    <nav className="flex flex-col gap-4" aria-label="Admin sections">
      {navGroups.map((group) => (
        <div key={group.label}>
          <p className="px-2.5 pb-1 text-[11px] font-medium tracking-[0.02em] text-muted">
            {group.label}
          </p>
          <ul className="flex flex-col gap-0.5">
            {group.items.map((item) => {
              const active = isActive(pathname, item.href);
              const Icon = item.icon;
              return (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    onClick={onNavigate}
                    aria-current={active ? "page" : undefined}
                    className={cn(
                      "flex h-8 items-center gap-2 rounded-[3px] px-2.5 text-[13px] transition-colors",
                      active
                        ? "bg-accent-soft font-medium text-accent"
                        : "text-secondary hover:bg-surface-muted hover:text-ink",
                    )}
                  >
                    <Icon size={16} />
                    {item.label}
                  </Link>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </nav>
  );
}

function SuspendedNav({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <Suspense fallback={<NavFallback />}>
      <NavLinks onNavigate={onNavigate} />
    </Suspense>
  );
}

function EnvironmentSwitch() {
  const { environment, setEnvironment } = useApp();
  const options: { value: Environment; label: string }[] = [
    { value: "sandbox", label: "Sandbox" },
    { value: "production", label: "Production" },
  ];
  return (
    <div
      role="group"
      aria-label="Active environment"
      className="inline-flex items-center gap-0.5 rounded-control border border-border bg-surface-muted p-0.5"
    >
      {options.map((option) => {
        const active = environment === option.value;
        return (
          <button
            key={option.value}
            type="button"
            aria-pressed={active}
            onClick={() => setEnvironment(option.value)}
            className={cn(
              "rounded-[3px] px-2 py-1 text-[12px] font-medium transition-colors",
              active
                ? option.value === "production"
                  ? "bg-surface text-ink shadow-[var(--shadow-raised)]"
                  : "bg-surface text-secondary shadow-[var(--shadow-raised)]"
                : "text-muted hover:text-ink",
            )}
          >
            {option.label}
          </button>
        );
      })}
    </div>
  );
}

const themeOptions: { value: ThemeMode; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "Match system" },
];

function AccountMenu() {
  const { actor, notify, theme, setTheme } = useApp();
  return (
    <DropdownRoot>
      <DropdownTrigger asChild>
        <Button variant="ghost" className="gap-2 pl-1.5 pr-2">
          <span
            aria-hidden="true"
            className="flex h-6 w-6 items-center justify-center rounded-full bg-ink text-[11px] font-medium text-canvas"
          >
            OP
          </span>
          <span className="hidden text-[12px] text-secondary sm:inline">
            {actor}
          </span>
          <IconChevronDown size={14} />
        </Button>
      </DropdownTrigger>
      <DropdownContent>
        <DropdownLabel>Signed in as</DropdownLabel>
        <div className="px-2 pb-1.5">
          <p className="text-[13px] text-ink">{actor}</p>
          <p className="text-[12px] text-muted">role: operator</p>
        </div>
        <DropdownSeparator />
        <DropdownLabel>Theme</DropdownLabel>
        <DropdownRadioGroup
          value={theme}
          onValueChange={(value) => setTheme(value as ThemeMode)}
        >
          {themeOptions.map((option) => (
            <DropdownRadioItem key={option.value} value={option.value}>
              {option.label}
            </DropdownRadioItem>
          ))}
        </DropdownRadioGroup>
        <DropdownSeparator />
        <DropdownItem
          onSelect={() => {
            void navigator.clipboard?.writeText(actor);
            notify("Account email copied to clipboard.");
          }}
        >
          Copy account email
        </DropdownItem>
        <DropdownItem
          onSelect={() =>
            notify("Sign out is not wired in the demo build. No session exists.")
          }
        >
          Sign out (demo build)
        </DropdownItem>
      </DropdownContent>
    </DropdownRoot>
  );
}

function DemoNotice() {
  return (
    <span className="hidden items-center rounded-[3px] border border-border bg-surface-muted px-2 py-1 text-[11px] font-medium text-secondary md:inline-flex">
      Demo build: sample data, no API connection
    </span>
  );
}

function MobileNav() {
  const [open, setOpen] = useState(false);
  return (
    <DialogRoot open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="secondary" size="sm">
          Sections
        </Button>
      </DialogTrigger>
      <DialogContent title="Navigation">
        <SuspendedNav onNavigate={() => setOpen(false)} />
      </DialogContent>
    </DialogRoot>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-screen">
      <aside className="fixed inset-y-0 left-0 hidden w-[224px] flex-col justify-between border-r border-border bg-surface lg:flex">
        <div className="p-3">
          <Link href="/" className="mb-4 flex flex-col px-2.5 py-1">
            <span className="text-[15px] font-semibold tracking-[-0.01em] text-ink">
              openpay.id
            </span>
            <span className="text-[11px] text-muted">admin console</span>
          </Link>
          <SuspendedNav />
        </div>
        <div className="border-t border-border-subtle p-3">
          <p className="px-2.5 text-[11px] text-muted">
            Design review build
          </p>
        </div>
      </aside>

      <div className="lg:pl-[224px]">
        <div className="sticky top-0 z-40 border-b border-border bg-surface">
          <div className="flex h-12 items-center justify-between gap-3 px-4 lg:px-6">
            <div className="flex items-center gap-3 lg:hidden">
              <Link href="/" className="text-[14px] font-semibold text-ink">
                openpay.id
              </Link>
              <MobileNav />
            </div>
            <div className="hidden lg:block">
              <DemoNotice />
            </div>
            <div className="flex items-center gap-2">
              <EnvironmentSwitch />
              <AccountMenu />
            </div>
          </div>
        </div>
        <main className="mx-auto max-w-[1320px] px-4 py-5 lg:px-6">
          <div className="mb-4 lg:hidden">
            <DemoNotice />
          </div>
          {children}
        </main>
      </div>
    </div>
  );
}
