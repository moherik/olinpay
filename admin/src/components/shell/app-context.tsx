"use client";

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import {
  merchants as seedMerchants,
  transactions as seedTransactions,
  type Environment,
  type Merchant as MerchantRecord,
  type Transaction,
} from "@/lib/demo-data";
import {
  readStoredTheme,
  setTheme as persistTheme,
  subscribe,
  type ThemeMode,
} from "@/lib/theme";

type Toast = { id: number; message: string };

export type Merchant = MerchantRecord;

/* Stable reference: useSyncExternalStore re-subscribes if this identity changes. */
const serverTheme = () => "system" as ThemeMode;

type AppContextValue = {
  environment: Environment;
  setEnvironment: (environment: Environment) => void;
  theme: ThemeMode;
  setTheme: (theme: ThemeMode) => void;
  actor: string;
  notify: (message: string) => void;
  merchants: Merchant[];
  addMerchant: (merchant: Merchant) => void;
  setMerchantStatus: (id: string, status: Merchant["status"]) => void;
  transactions: Transaction[];
  updateTransaction: (id: string, patch: Partial<Transaction>) => void;
};

const AppContext = createContext<AppContextValue | null>(null);

export function useApp() {
  const value = useContext(AppContext);
  if (!value) throw new Error("useApp must be used inside AppProvider");
  return value;
}

export function AppProvider({ children }: { children: React.ReactNode }) {
  const [environment, setEnvironment] = useState<Environment>("production");
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [merchants, setMerchants] = useState<Merchant[]>(seedMerchants);
  const [transactions, setTransactions] = useState<Transaction[]>(seedTransactions);
  const nextId = useRef(1);

  /*
   * The value is read from storage, not mirrored into state, so the class the
   * head script already applied and the value React renders can never disagree.
   * During hydration React uses the server snapshot ("system"), then re-renders
   * with the stored value once mounted.
   */
  const theme = useSyncExternalStore(subscribe, readStoredTheme, serverTheme);
  const setTheme = useCallback((mode: ThemeMode) => persistTheme(mode), []);

  const notify = useCallback((message: string) => {
    const id = nextId.current++;
    setToasts((current) => [...current, { id, message }]);
    setTimeout(() => {
      setToasts((current) => current.filter((toast) => toast.id !== id));
    }, 4500);
  }, []);

  const addMerchant = useCallback((merchant: Merchant) => {
    setMerchants((current) => [merchant, ...current]);
  }, []);

  const setMerchantStatus = useCallback(
    (id: string, status: Merchant["status"]) => {
      setMerchants((current) =>
        current.map((merchant) =>
          merchant.id === id ? { ...merchant, status } : merchant,
        ),
      );
    },
    [],
  );

  const updateTransaction = useCallback(
    (id: string, patch: Partial<Transaction>) => {
      setTransactions((current) =>
        current.map((row) => (row.id === id ? { ...row, ...patch } : row)),
      );
    },
    [],
  );

  const value = useMemo(
    () => ({
      environment,
      setEnvironment,
      theme,
      setTheme,
      actor: "operator@example.com",
      notify,
      merchants,
      addMerchant,
      setMerchantStatus,
      transactions,
      updateTransaction,
    }),
    [
      environment,
      theme,
      setTheme,
      notify,
      merchants,
      addMerchant,
      setMerchantStatus,
      transactions,
      updateTransaction,
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
