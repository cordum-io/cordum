import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { useUiStore } from "../state/ui";

// ---------------------------------------------------------------------------
// Shortcut config (exported so the help overlay can enumerate them)
// ---------------------------------------------------------------------------

export interface Shortcut {
  keys: [string, string];
  label: string;
  description: string;
  action: string;
}

export const SHORTCUTS: Shortcut[] = [
  { keys: ["g", "o"], label: "g o", description: "Go to Overview", action: "/" },
  { keys: ["g", "j"], label: "g j", description: "Go to Jobs", action: "/jobs" },
  { keys: ["g", "w"], label: "g w", description: "Go to Workflows", action: "/workflows" },
  { keys: ["g", "a"], label: "g a", description: "Go to Agents", action: "/agents" },
  { keys: ["g", "k"], label: "g k", description: "Go to Approvals", action: "/approvals" },
  { keys: ["g", "p"], label: "g p", description: "Go to Policies", action: "/policies" },
  { keys: ["g", "x"], label: "g x", description: "Go to Packs", action: "/packs" },
  { keys: ["g", "d"], label: "g d", description: "Go to Dead Letters", action: "/dlq" },
  { keys: ["g", "l"], label: "g l", description: "Go to Audit Log", action: "/audit" },
  { keys: ["g", "s"], label: "g s", description: "Go to Settings", action: "/settings" },
];

// Build a lookup: second-key -> action
const NAV_MAP = new Map<string, string>(
  SHORTCUTS.map((s) => [s.keys[1], s.action]),
);

// ---------------------------------------------------------------------------
// Hook
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
  const prefixRef = useRef<string | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout>>();

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
        const action = NAV_MAP.get(key);
        if (action) {
          e.preventDefault();
          navigate(action);
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
  }, [navigate]);
}
