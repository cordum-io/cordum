import { create } from "zustand";

type Theme = "light" | "dark";
export type AgentsView = "table" | "cards";

interface UiState {
  theme: Theme;
  globalSearch: string;
  commandOpen: boolean;
  agentsView: AgentsView;
  toggleTheme: () => void;
  setGlobalSearch: (value: string) => void;
  setCommandOpen: (open: boolean) => void;
  setAgentsView: (view: AgentsView) => void;
}

const stored =
  typeof window !== "undefined"
    ? (window.localStorage.getItem("cordum-theme") as Theme | null)
    : null;

const storedAgentsView =
  typeof window !== "undefined"
    ? (window.localStorage.getItem("cordum-agents-view") as AgentsView | null)
    : null;

export const useUiStore = create<UiState>((set) => ({
  theme: stored === "dark" ? "dark" : "light",
  globalSearch: "",
  commandOpen: false,
  agentsView: storedAgentsView === "cards" ? "cards" : "table",
  toggleTheme: () =>
    set((s) => ({ theme: s.theme === "dark" ? "light" : "dark" })),
  setGlobalSearch: (value) => set({ globalSearch: value }),
  setCommandOpen: (open) => set({ commandOpen: open }),
  setAgentsView: (view) => {
    window.localStorage.setItem("cordum-agents-view", view);
    set({ agentsView: view });
  },
}));
