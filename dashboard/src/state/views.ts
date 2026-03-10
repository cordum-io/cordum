import { create } from "zustand";

export type RunView = {
  id: string;
  name: string;
  filters: {
    status?: string;
    workflowId?: string;
    query?: string;
  };
};

type ViewState = {
  views: RunView[];
  addView: (view: RunView) => void;
  removeView: (id: string) => void;
};

const STORAGE_KEY = "cordum.dashboard.runviews";

function loadViews(): RunView[] {
  if (typeof window === "undefined") {
    return [];
  }
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) {
      return [];
    }
    const data = JSON.parse(raw) as RunView[];
    return Array.isArray(data) ? data : [];
  } catch {
    return [];
  }
}

function persist(views: RunView[]) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(views));
  } catch {
    // Ignore persistence errors.
  }
}

export const useViewStore = create<ViewState>((set, get) => ({
  views: loadViews(),
  addView: (view) => {
    const next = [view, ...get().views].slice(0, 12);
    persist(next);
    set({ views: next });
  },
  removeView: (id) => {
    const next = get().views.filter((view) => view.id !== id);
    persist(next);
    set({ views: next });
  },
}));
