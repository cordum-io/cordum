import { create } from "zustand";

export type ToastType = "success" | "error" | "warning" | "info";

export interface Toast {
  id: string;
  type: ToastType;
  title: string;
  description?: string;
  duration: number;
  dismissible: boolean;
}

export interface AddToastOptions {
  type: ToastType;
  title: string;
  description?: string;
  duration?: number;
  dismissible?: boolean;
}

interface ToastState {
  toasts: Toast[];
  addToast: (options: AddToastOptions) => string;
  dismissToast: (id: string) => void;
}

const MAX_TOASTS = 5;

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  addToast: (options) => {
    const id = crypto.randomUUID();
    const toast: Toast = {
      id,
      type: options.type,
      title: options.title,
      description: options.description,
      duration: options.duration ?? 5000,
      dismissible: options.dismissible ?? true,
    };
    set((s) => ({
      toasts: [toast, ...s.toasts].slice(0, MAX_TOASTS),
    }));
    return id;
  },
  dismissToast: (id) =>
    set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}));
