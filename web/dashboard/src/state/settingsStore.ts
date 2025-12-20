import { create } from "zustand";
import { defaultSettings, loadSettings, saveSettings, type StudioSettings } from "../lib/env";
import { useAuthStore } from "./authStore";

type SettingsState = StudioSettings & {
  setApiBase: (apiBase: string) => void;
  setWsBase: (wsBase: string) => void;
  setApiKey: (apiKey: string) => void;
  reset: () => void;
};

export const useSettingsStore = create<SettingsState>((set) => {
  const initial = loadSettings();

  const persist = (next: Partial<StudioSettings>) => {
    useAuthStore.getState().reset();
    set((cur) => {
      const merged: StudioSettings = { ...cur, ...next };
      saveSettings(merged);
      return merged;
    });
  };

  return {
    ...initial,
    setApiBase: (apiBase) => persist({ apiBase }),
    setWsBase: (wsBase) => persist({ wsBase }),
    setApiKey: (apiKey) => persist({ apiKey }),
    reset: () => {
      const fresh = defaultSettings();
      saveSettings(fresh);
      useAuthStore.getState().reset();
      set(fresh);
    },
  };
});
