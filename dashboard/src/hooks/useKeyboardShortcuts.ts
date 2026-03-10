import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { useUiStore } from "../state/ui";

// ---------------------------------------------------------------------------
// Canonical g+key navigation map (single source of truth)
// AppShell re-exports this so tests can import from either location.
// ---------------------------------------------------------------------------

export const G_KEY_MAP: Record<string, string> = {
  h: "/",
  o: "/",
  j: "/jobs",
  w: "/workflows",
  a: "/agents",
  k: "/approvals",
  p: "/govern/input-rules",
  b: "/govern/bundles",
  x: "/packs",
  s: "/settings",
  d: "/dlq",
  l: "/audit",
};

// ---------------------------------------------------------------------------
// Shortcut config (derived from G_KEY_MAP)
// ---------------------------------------------------------------------------

export interface Shortcut {
  keys: [string, string];
  label: string;
  description: string;
  action: string;
}

const ROUTE_LABELS: Record<string, string> = {
  "/": "Overview",
  "/jobs": "Jobs",
  "/workflows": "Workflows",
  "/agents": "Agents",
  "/approvals": "Approvals",
  "/govern/input-rules": "Input Rules",
  "/govern/bundles": "Bundles",
  "/packs": "Packs",
  "/settings": "Settings",
  "/dlq": "Dead Letters",
  "/audit": "Audit Log",
};

// Deduplicate routes (h and o both point to /) — keep first occurrence
const seen = new Set<string>();
export const SHORTCUTS: Shortcut[] = Object.entries(G_KEY_MAP)
  .filter(([, route]) => {
    if (seen.has(route)) return false;
    seen.add(route);
    return true;
  })
  .map(([key, route]) => ({
    keys: ["g", key] as [string, string],
    label: `g ${key}`,
    description: `Go to ${ROUTE_LABELS[route] ?? route}`,
    action: route,
  }));

// ---------------------------------------------------------------------------
// Hook — mounts ? help overlay toggle, Escape close, and g+key navigation
// ---------------------------------------------------------------------------

function isEditableTarget(el: EventTarget | null): boolean {
  if (!el || !(el instanceof HTMLElement)) return false;
  const tag = el.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if (el.isContentEditable) return true;
  return false;
}

export function useKeyboardShortcuts() {
  const navigate = useNavigate();
  const navigateRef = useRef(navigate);
  navigateRef.current = navigate;
  const prefixRef = useRef<string | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      // Skip when typing in inputs
      if (isEditableTarget(e.target)) return;
      // Skip when modifier keys held (except Shift for ?)
      if (e.ctrlKey || e.metaKey || e.altKey) return;

      const key = e.key;

      // ? toggles help overlay (Shift+/ on most keyboards)
      if (key === "?") {
        e.preventDefault();
        useUiStore.getState().setShortcutsHelpOpen(
          !useUiStore.getState().shortcutsHelpOpen,
        );
        return;
      }

      // Escape closes help overlay
      if (key === "Escape") {
        if (useUiStore.getState().shortcutsHelpOpen) {
          useUiStore.getState().setShortcutsHelpOpen(false);
          e.preventDefault();
        }
        return;
      }

      // Two-key sequence: first key is "g"
      if (prefixRef.current === "g") {
        prefixRef.current = null;
        clearTimeout(timerRef.current);
        const action = G_KEY_MAP[key];
        if (action) {
          e.preventDefault();
          navigateRef.current(action);
        }
        return;
      }

      // Start prefix window on "g"
      if (key === "g") {
        prefixRef.current = "g";
        clearTimeout(timerRef.current);
        timerRef.current = setTimeout(() => {
          prefixRef.current = null;
        }, 1000);
        return;
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      clearTimeout(timerRef.current);
    };
  }, []);
}

/** @internal exported for unit tests */
export const __keyboardShortcutsInternal = {
  isEditableTarget,
  NAV_MAP: new Map(Object.entries(G_KEY_MAP)),
};
