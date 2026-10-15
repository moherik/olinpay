/*
 * Theme lives outside React because the class it toggles has to be on the
 * document before the first paint. A blocking script in the document head reads
 * storage and sets the class; React then reads the same value through
 * useSyncExternalStore, which keeps the server render ("system") and the client
 * render in agreement.
 *
 * Deliberately not a client module: the root layout imports the bootstrap string
 * during server rendering, and a "use client" boundary would turn every export
 * here into a client reference instead of a value.
 */

export type ThemeMode = "light" | "dark" | "system";

export const THEME_KEY = "openpay-admin-theme";

const listeners = new Set<() => void>();
let isMediaBound = false;

function emit() {
  for (const listener of listeners) listener();
}

export function subscribe(listener: () => void) {
  listeners.add(listener);

  if (!isMediaBound && typeof window !== "undefined") {
    isMediaBound = true;
    window
      .matchMedia("(prefers-color-scheme: dark)")
      .addEventListener("change", () => {
        if (readStoredTheme() === "system") {
          applyTheme("system");
          emit();
        }
      });
  }

  return () => {
    listeners.delete(listener);
  };
}

export function readStoredTheme(): ThemeMode {
  if (typeof window === "undefined") return "system";
  try {
    const value = window.localStorage.getItem(THEME_KEY);
    return value === "light" || value === "dark" || value === "system"
      ? value
      : "system";
  } catch {
    return "system";
  }
}

export function resolveTheme(mode: ThemeMode): "light" | "dark" {
  if (mode === "light" || mode === "dark") return mode;
  if (typeof window === "undefined") return "light";
  return window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

export function applyTheme(mode: ThemeMode) {
  const resolved = resolveTheme(mode);
  const root = document.documentElement;
  root.classList.toggle("dark", resolved === "dark");
  root.style.colorScheme = resolved;
}

export function setTheme(mode: ThemeMode) {
  try {
    window.localStorage.setItem(THEME_KEY, mode);
  } catch {
    /* Storage can be blocked. The choice still applies for this tab. */
  }
  applyTheme(mode);
  emit();
}

/*
 * Inlined into the document head. Must stay self contained and dependency free:
 * it runs before any module loads, and a throw here would leave the page in the
 * wrong theme. The storage key is interpolated so it cannot drift from THEME_KEY.
 */
export const THEME_BOOTSTRAP_SCRIPT = `(function(){try{var k=${JSON.stringify(
  THEME_KEY,
)};var s=localStorage.getItem(k);var dark=s==="dark"||(s!=="light"&&matchMedia("(prefers-color-scheme: dark)").matches);var e=document.documentElement;e.classList.toggle("dark",dark);e.style.colorScheme=dark?"dark":"light";}catch(error){}})();`;
