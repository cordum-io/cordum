import { useEffect, useRef, useCallback } from "react";

export const FOCUSABLE_SELECTOR =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

interface UseDialogA11yOptions {
  enabled?: boolean;
  initialFocusSelector?: string;
  restoreFocus?: boolean;
}

/**
 * Manages dialog accessibility: focus trap, Escape key, and initial focus.
 * Returns a ref to attach to the dialog content element.
 */
export function useDialogA11y(
  onClose: () => void,
  options: UseDialogA11yOptions = {},
) {
  const {
    enabled = true,
    initialFocusSelector,
    restoreFocus = true,
  } = options;
  const ref = useRef<HTMLDivElement>(null);
  const previousFocusedElementRef = useRef<HTMLElement | null>(null);

  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (!enabled) return;

      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
        return;
      }

      if (e.key !== "Tab") return;

      const el = ref.current;
      if (!el) return;

      const focusable = Array.from(
        el.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR),
      );
      if (focusable.length === 0) return;

      const first = focusable[0];
      const last = focusable[focusable.length - 1];

      if (e.shiftKey) {
        if (document.activeElement === first) {
          e.preventDefault();
          last.focus();
        }
      } else {
        if (document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    },
    [enabled, onClose],
  );

  useEffect(() => {
    if (!enabled) return;

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [enabled, handleKeyDown]);

  useEffect(() => {
    if (!enabled) {
      previousFocusedElementRef.current = null;
      return;
    }

    previousFocusedElementRef.current =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;

    const el = ref.current;
    if (!el) {
      return () => {
        previousFocusedElementRef.current = null;
      };
    }

    const preferred = initialFocusSelector
      ? el.querySelector<HTMLElement>(initialFocusSelector)
      : null;
    const first = preferred ?? el.querySelector<HTMLElement>(FOCUSABLE_SELECTOR);

    if (first) {
      first.focus();
    } else {
      el.setAttribute("tabindex", "-1");
      el.focus();
    }

    return () => {
      if (restoreFocus) {
        const previous = previousFocusedElementRef.current;
        if (previous && document.contains(previous)) {
          previous.focus();
        }
      }
      previousFocusedElementRef.current = null;
    };
  }, [enabled, initialFocusSelector, restoreFocus]);

  return ref;
}
