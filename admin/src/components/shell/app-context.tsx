"use client";

import { useRouter } from "next/navigation";
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { Environment, Merchant } from "@/lib/demo-data";
import {
  ApiError,
  apiGet,
  apiMutate,
  fetchMe,
  mapMerchant,
  type RawMerchantRow,
} from "@/lib/api";
import {
  readStoredTheme,
  setTheme as persistTheme,
  subscribe,
  type ThemeMode,
} from "@/lib/theme";

type Toast = { id: number; message: string };

export type Actor = {
  id: string;
  email: string;
  name: string;
  role: "owner" | "operator" | "tech";
};

export type AuthState =
  | { ready: false }
  | { ready: true; authenticated: false }
  | { ready: true; authenticated: true; user: Actor; flags: Record<string, boolean> };

/* Stable reference: useSyncExternalStore re-subscribes if this identity changes. */
const serverTheme = () => "system" as ThemeMode;

type AppContextValue = {
  environment: Environment;
  setEnvironment: (environment: Environment) => void;
  theme: ThemeMode;
  setTheme: (theme: ThemeMode) => void;
  notify: (message: string) => void;
  /* Auth, resolved once on mount by the console frame. */
  auth: AuthState;
  refreshAuth: () => Promise<AuthState>;
  signOut: () => Promise<void>;
  can: (flag: string) => boolean;
  /* Merchants, loaded from the API. */
  merchants: Merchant[];
  merchantsReady: boolean;
  reloadMerchants: () => Promise<void>;
  createMerchant: (
    name: string,
    callbackUrl: string,
  ) => Promise<{ merchant: Merchant; webhookSecret?: string }>;
  setMerchantStatus: (id: string, status: Merchant["status"]) => Promise<void>;
};

const AppContext = createContext<AppContextValue | null>(null);

export function useApp() {
  const value = useContext(AppContext);
  if (!value) throw new Error("useApp must be used inside AppProvider");
  return value;
}

export function AppProvider({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [environment, setEnvironment] = useState<Environment>("sandbox");
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [auth, setAuth] = useState<AuthState>({ ready: false });
  const [merchants, setMerchants] = useState<Merchant[]>([]);
  const [merchantsReady, setMerchantsReady] = useState(false);
  const nextToastId = useRef(1);

  /*
   * The value is read from storage, not mirrored into state, so the class the
   * head script already applied and the value React renders can never disagree.
   * During hydration React uses the server snapshot ("system"), then re-renders
   * with the stored value once mounted.
   */
  const theme = useSyncExternalStore(subscribe, readStoredTheme, serverTheme);
  const setTheme = useCallback((mode: ThemeMode) => persistTheme(mode), []);

  const notify = useCallback((message: string) => {
    const id = nextToastId.current++;
    setToasts((current) => [...current, { id, message }]);
    setTimeout(() => {
      setToasts((current) => current.filter((toast) => toast.id !== id));
    }, 4500);
  }, []);

  const refreshAuth = useCallback(async (): Promise<AuthState> => {
    try {
      const { data } = await fetchMe();
      const next: AuthState = {
        ready: true,
        authenticated: true,
        user: {
          id: data.user.id,
          email: data.user.email,
          name: data.user.name || data.user.email,
          role: data.user.role,
        },
        flags: data.flags ?? {},
      };
      setAuth(next);
      return next;
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        const next: AuthState = { ready: true, authenticated: false };
        setAuth(next);
        return next;
      }
      throw error;
    }
  }, []);

  const signOut = useCallback(async () => {
    try {
      await apiMutate("POST", "/logout");
    } finally {
      setAuth({ ready: true, authenticated: false });
      try {
        router.replace("/login");
      } catch {
        /* navigation before mount is a no-op */
      }
    }
  }, [router]);

  const reloadMerchants = useCallback(async () => {
    const { data } = await apiGet<RawMerchantRow[]>("/merchants");
    setMerchants(data.map(mapMerchant));
    setMerchantsReady(true);
  }, []);

  const createMerchant = useCallback(
    async (name: string, callbackUrl: string) => {
      const { data, meta } = await apiMutate<RawMerchantRow>(
        "POST",
        "/merchants",
        { name, callback_url: callbackUrl },
      );
      const created = mapMerchant({
        ...data,
        active_keys: 0,
        transactions_30d: 0,
        environments: [],
      });
      setMerchants((current) => [created, ...current]);
      return {
        merchant: created,
        webhookSecret:
          typeof meta.webhook_secret === "string"
            ? (meta.webhook_secret as string)
            : undefined,
      };
    },
    [],
  );

  const setMerchantStatus = useCallback(
    async (id: string, status: Merchant["status"]) => {
      await apiMutate("PATCH", `/merchants/${id}`, {
        status: status === "active" ? "active" : "suspended",
      });
      setMerchants((current) =>
        current.map((merchant) =>
          merchant.id === id ? { ...merchant, status } : merchant,
        ),
      );
    },
    [],
  );

  const can = useCallback(
    (flag: string) => {
      if (auth.ready && auth.authenticated) {
        return auth.flags[flag] !== false;
      }
      return false;
    },
    [auth],
  );

  const value = useMemo(
    () => ({
      environment,
      setEnvironment,
      theme,
      setTheme,
      notify,
      auth,
      refreshAuth,
      signOut,
      can,
      merchants,
      merchantsReady,
      reloadMerchants,
      createMerchant,
      setMerchantStatus,
    }),
    [
      environment,
      theme,
      setTheme,
      notify,
      auth,
      refreshAuth,
      signOut,
      can,
      merchants,
      merchantsReady,
      reloadMerchants,
      createMerchant,
      setMerchantStatus,
    ],
  );

  return (
    <AppContext.Provider value={value}>
      {children}
      <div
        role="status"
        aria-live="polite"
        className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-[320px] flex-col gap-2"
      >
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className="rounded-panel border border-border bg-surface px-3 py-2 text-[13px] text-ink shadow-[var(--shadow-overlay)]"
          >
            {toast.message}
          </div>
        ))}
      </div>
    </AppContext.Provider>
  );
}


