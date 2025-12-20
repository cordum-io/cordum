import { create } from "zustand";

type CommandPaletteState = {
  open: boolean;
  openPalette: () => void;
  close: () => void;
  toggle: () => void;
};

export const useCommandPaletteStore = create<CommandPaletteState>((set) => ({
  open: false,
  openPalette: () => set({ open: true }),
  close: () => set({ open: false }),
  toggle: () => set((cur) => ({ open: !cur.open })),
}));

