import { create } from "zustand";

type ThemeMode = "light" | "dark";

type UiState = {
  globalSearch: string;
  commandOpen: boolean;
  theme: ThemeMode;
  setGlobalSearch: (value: string) => void;
  setCommandOpen: (open: boolean) => void;
  setTheme: (theme: ThemeMode) => void;
  toggleTheme: () => void;
};

const THEME_KEY = "cordum-theme";

const resolveInitialTheme = (): ThemeMode => {
  if (typeof window === "undefined") {
    return "light";
  }
  const stored = window.localStorage.getItem(THEME_KEY);
  if (stored === "light" || stored === "dark") {
    return stored;
  }
  if (window.matchMedia?.("(prefers-color-scheme: dark)").matches) {
    return "dark";
  }
  return "light";
};

export const useUiStore = create<UiState>((set) => ({
  globalSearch: "",
  commandOpen: false,
  theme: resolveInitialTheme(),
  setGlobalSearch: (value) => set({ globalSearch: value }),
  setCommandOpen: (open) => set({ commandOpen: open }),
  setTheme: (theme) => set({ theme }),
  toggleTheme: () => set((state) => ({ theme: state.theme === "dark" ? "light" : "dark" })),
}));
