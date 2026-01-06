import { create } from "zustand";

export type PinnedItem = {
  id: string;
  label: string;
  type: "workflow" | "run" | "pool" | "system";
  detail?: string;
};

type PinState = {
  items: PinnedItem[];
  addPin: (item: PinnedItem) => void;
  removePin: (id: string) => void;
};

const STORAGE_KEY = "coretex.dashboard.pins";

function loadPins(): PinnedItem[] {
  if (typeof window === "undefined") {
    return [];
  }
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) {
      return [];
    }
    const data = JSON.parse(raw) as PinnedItem[];
    return Array.isArray(data) ? data : [];
  } catch {
    return [];
  }
}

function persist(items: PinnedItem[]) {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(items));
  } catch {
    // Ignore persistence errors.
  }
}

export const usePinStore = create<PinState>((set, get) => ({
  items: loadPins(),
  addPin: (item) => {
    const existing = get().items;
    if (existing.some((p) => p.id === item.id)) {
      return;
    }
    const next = [item, ...existing].slice(0, 12);
    persist(next);
    set({ items: next });
  },
  removePin: (id) => {
    const next = get().items.filter((item) => item.id !== id);
    persist(next);
    set({ items: next });
  },
}));
