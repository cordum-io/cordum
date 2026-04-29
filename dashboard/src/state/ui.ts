import { create } from "zustand";
import { broadcastSync } from "../hooks/useCrossTabSync";
import { safeLocalStorage } from "../lib/storage";

export type Theme = "light" | "dark" | "system";
export type ResolvedTheme = "light" | "dark";
export type AgentsView = "table" | "cards";

function resolveTheme(pref: Theme): ResolvedTheme {
  if (pref === "system") {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") {
      return "light";
    }
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  return pref;
}

function isTheme(value: string | null): value is Theme {
  return value === "light" || value === "dark" || value === "system";
}

interface UiState {
  theme: Theme;
  resolvedTheme: ResolvedTheme;
  globalSearch: string;
  commandOpen: boolean;
  agentsView: AgentsView;
  shortcutsHelpOpen: boolean;
  toggleTheme: () => void;
  setTheme: (theme: Theme) => void;
  syncSystemTheme: () => void;
  setGlobalSearch: (value: string) => void;
  setCommandOpen: (open: boolean) => void;
  setAgentsView: (view: AgentsView) => void;
  setShortcutsHelpOpen: (open: boolean) => void;
}

const rawStoredTheme = safeLocalStorage.getItem("cordum-theme");
const stored = isTheme(rawStoredTheme) ? rawStoredTheme : null;

const storedAgentsView = safeLocalStorage.getItem("cordum-agents-view") as AgentsView | null;

// Default to 'system' if no preference saved
const initialTheme: Theme = stored ?? "system";

export const useUiStore = create<UiState>((set) => ({
  theme: initialTheme,
  resolvedTheme: resolveTheme(initialTheme),
  globalSearch: "",
  commandOpen: false,
  agentsView: storedAgentsView === "cards" ? "cards" : "table",
  shortcutsHelpOpen: false,
  toggleTheme: () =>
    set((s) => {
      const next: Theme =
        s.theme === "light" ? "dark" : s.theme === "dark" ? "system" : "light";
      const resolved = resolveTheme(next);
      safeLocalStorage.setItem("cordum-theme", next);
      broadcastSync({ type: "theme-change", theme: next });
      return { theme: next, resolvedTheme: resolved };
    }),
  setTheme: (theme) =>
    set(() => {
      const resolved = resolveTheme(theme);
      safeLocalStorage.setItem("cordum-theme", theme);
      return { theme, resolvedTheme: resolved };
    }),
  syncSystemTheme: () =>
    set((s) => {
      if (s.theme !== "system") return s;
      return { resolvedTheme: resolveTheme("system") };
    }),
  setGlobalSearch: (value) => set({ globalSearch: value }),
  setCommandOpen: (open) => set({ commandOpen: open }),
  setAgentsView: (view) => {
    safeLocalStorage.setItem("cordum-agents-view", view);
    set({ agentsView: view });
  },
  setShortcutsHelpOpen: (open) => set({ shortcutsHelpOpen: open }),
}));
