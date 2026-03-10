import { useEffect, useRef } from "react";
import { toast } from "sonner";
import { useToastStore } from "@/state/toast";
import type { ToastType } from "@/state/toast";

const sonnerMethod: Record<ToastType, typeof toast.success> = {
  success: toast.success,
  error: toast.error,
  warning: toast.warning,
  info: toast.info,
};

/**
 * Bridges Zustand useToastStore → sonner.
 * Subscribes to new toasts added via addToast() and forwards them
 * to sonner so they actually render in the UI.
 */
export function ToastBridge() {
  const firedRef = useRef<Set<string>>(new Set());
  const toasts = useToastStore((s) => s.toasts);
  const dismissToast = useToastStore((s) => s.dismissToast);

  useEffect(() => {
    for (const t of toasts) {
      if (firedRef.current.has(t.id)) continue;
      firedRef.current.add(t.id);

      const method = sonnerMethod[t.type] ?? toast.info;
      method(t.title, {
        description: t.description,
        duration: t.duration,
      });

      dismissToast(t.id);
    }
  }, [toasts, dismissToast]);

  return null;
}
