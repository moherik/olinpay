"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, useState } from "react";
import { IconChevronDown } from "@/components/icons";
import { Button } from "@/components/ui/button";
import {
  DialogClose,
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

const navItems = [
  { href: "/", label: "Overview" },
  { href: "/transactions", label: "Transactions" },
  { href: "/webhooks", label: "Webhooks" },
  { href: "/merchants", label: "Merchants" },
  { href: "/routing", label: "Routing" },
  { href: "/providers", label: "Providers" },
  { href: "/api-keys", label: "API keys" },
  { href: "/users", label: "Users" },
  { href: "/audit", label: "Audit" },
];

function isActive(pathname: string, href: string) {
  if (href === "/") return pathname === "/";
  return pathname === href || pathname.startsWith(`${href}/`);
}

/* Static stand-in while the path is not yet known. It carries no active state. */
function NavFallback() {
  return (
    <ul aria-hidden="true" className="flex items-center gap-1">
      {navItems.map((item) => (
        <li key={item.href} className="flex h-10 items-center px-2.5 text-[13px] text-muted">
          {item.label}
        </li>
      ))}
    </ul>
  );
}

function NavLinks() {
  const pathname = usePathname();
  return (
    <nav aria-label="Admin sections" className="flex items-center gap-1 overflow-x-auto">
      {navItems.map((item) => {
        const active = isActive(pathname, item.href);
        return (
          <Link
            key={item.href}
            href={item.href}
            aria-current={active ? "page" : undefined}
            className={cn(
              "flex h-10 shrink-0 items-center border-b-2 px-2.5 text-[13px] transition-colors",
              "border-transparent",
              active
                ? "-mb-px border-ink font-medium text-ink"
                : "text-secondary hover:text-ink",
            )}
          >
            {item.label}
          </Link>
        );
      })}
    </nav>
  );
}

function SuspendedNav() {
  return (
    <Suspense fallback={<NavFallback />}>
      <NavLinks />
    </Suspense>
  );
}

function MobileNavigation() {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);

  return (
    <DialogRoot open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          className="h-11 px-3 text-[13px] lg:hidden"
        >
          Menu
        </Button>
      </DialogTrigger>
      <DialogContent
        title="Sections"
        description="Choose a console area."
        className="lg:hidden"
        closeClassName="h-11 w-11 p-0"
      >
        <nav aria-label="Admin sections">
          <ul className="flex flex-col gap-1">
            {navItems.map((item) => {
              const active = isActive(pathname, item.href);
              return (
                <li key={item.href}>
                  <DialogClose asChild>
                    <Link
                      href={item.href}
                      aria-current={active ? "page" : undefined}
                      className={cn(
                        "flex min-h-11 items-center rounded-control px-3 text-[13px] transition-colors",
                        active
                          ? "bg-accent-soft font-medium text-ink"
                          : "text-secondary hover:bg-surface-muted hover:text-ink",
                      )}
                    >
                      {item.label}
                    </Link>
                  </DialogClose>
                </li>
              );
            })}
          </ul>
        </nav>
      </DialogContent>
    </DialogRoot>
  );
}

function MobileNavigationFallback() {
  return <span aria-hidden="true" className="h-11 w-[58px] lg:hidden" />;
}

function SuspendedMobileNavigation() {
  return (
    <Suspense fallback={<MobileNavigationFallback />}>
      <MobileNavigation />
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
              "h-11 rounded-[3px] px-2 text-[12px] font-medium transition-colors lg:h-6",
              active
                ? "bg-surface text-ink shadow-[var(--shadow-raised)]"
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
  const { auth, signOut, notify, theme, setTheme } = useApp();
  const user = auth.ready && auth.authenticated ? auth.user : null;
  const initials =
    user === null
      ? "AW"
      : (user.name || user.email)
          .split(/[\s@.]+/)
          .filter(Boolean)
          .slice(0, 2)
          .map((part) => part[0]?.toUpperCase())
          .join("") || "AW";
  return (
    <DropdownRoot>
      <DropdownTrigger asChild>
        <Button
          variant="ghost"
          className="h-11 w-11 gap-2 px-0 lg:h-8 lg:w-auto lg:pl-1.5 lg:pr-2"
        >
          <span
            aria-hidden="true"
            className="flex h-6 w-6 items-center justify-center rounded-full bg-ink text-[11px] font-medium text-canvas"
          >
            {initials}
          </span>
          <span className="hidden text-[12px] text-secondary sm:inline">
            {user?.email}
          </span>
          <IconChevronDown size={14} />
        </Button>
      </DropdownTrigger>
      <DropdownContent>
        <DropdownLabel>Signed in as</DropdownLabel>
        <div className="px-2 pb-1.5">
          <p className="text-[13px] text-ink">{user?.name || user?.email}</p>
          <p className="text-[12px] text-muted">role: {user?.role}</p>
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
            void navigator.clipboard?.writeText(user?.email ?? "");
            notify("Account email copied to clipboard.");
          }}
        >
          Copy account email
        </DropdownItem>
        <DropdownItem
          onSelect={() => {
            void signOut();
            notify("Signed out.");
          }}
        >
          Sign out
        </DropdownItem>
      </DropdownContent>
    </DropdownRoot>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const { environment } = useApp();
  return (
    <div className="min-h-screen">
      <header
        className={cn(
          "sticky top-0 z-40 border-b border-border",
          environment === "production"
            ? "bg-production-wash"
            : "bg-sandbox-wash",
        )}
      >
        <div className="flex h-12 items-center justify-between gap-2 px-4 lg:gap-3 lg:px-6">
          <div className="flex min-w-0 items-center gap-3">
            <Link
              href="/"
              className="flex items-center gap-2"
              aria-label="AwsemPay console home"
            >
              <span
                aria-hidden="true"
                className="flex h-6 w-6 items-center justify-center rounded-[6px] bg-ink text-[12px] font-bold text-canvas"
              >
                A
              </span>
              <span className="text-[15px] font-semibold tracking-[-0.01em] text-ink">
                AwsemPay
              </span>
            </Link>
          </div>
          <div className="flex shrink-0 items-center gap-1 lg:gap-3">
            <SuspendedMobileNavigation />
            <EnvironmentSwitch />
            <AccountMenu />
          </div>
        </div>
        <div className="hidden h-full items-center px-4 lg:flex lg:px-6">
          <SuspendedNav />
        </div>
      </header>
      <main className="mx-auto min-w-0 max-w-330 px-4 py-5 lg:px-6">
        {children}
      </main>
    </div>
  );
}
