import { create } from "zustand";
import React from "react";

type InspectorState = {
  open: boolean;
  title?: string;
  body?: React.ReactNode;
  show: (title: string, body: React.ReactNode) => void;
  toggle: () => void;
  close: () => void;
};

export const useInspectorStore = create<InspectorState>((set) => ({
  open: false,
  title: undefined,
  body: undefined,
  show: (title, body) => set({ open: true, title, body }),
  toggle: () => set((cur) => ({ open: !cur.open })),
  close: () => set({ open: false }),
}));
