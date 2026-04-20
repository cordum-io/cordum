import { useEffect, useRef } from "react";
import { Keyboard } from "lucide-react";
import { useUiStore } from "../state/ui";
import { SHORTCUTS } from "../hooks/useKeyboardShortcuts";

export function KeyboardShortcutsHelp() {
  const open = useUiStore((s) => s.shortcutsHelpOpen);
  const setOpen = useUiStore((s) => s.setShortcutsHelpOpen);
  const dialogRef = useRef<HTMLDivElement>(null);

  // Close on Escape
  useEffect(() => {
    if (!open) return;
    function handleKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        setOpen(false);
      }
    }
    document.addEventListener("keydown", handleKey);
    return () => document.removeEventListener("keydown", handleKey);
  }, [open, setOpen]);

  if (!open) return null;

  const kbdClassName =
    "inline-flex min-w-[24px] items-center justify-center rounded bg-surface-2 px-2 py-0.5 font-mono text-xs text-ink";

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm"
      onClick={(e) => {
        if (e.target === e.currentTarget) setOpen(false);
      }}
    >
      <div
        ref={dialogRef}
        className="mx-4 w-full max-w-md rounded-2xl border border-border bg-card p-6 shadow-2xl"
      >
        {/* Header */}
        <div className="mb-4 flex items-center gap-2">
          <Keyboard className="h-5 w-5 text-accent" />
          <h2 className="text-lg font-semibold text-ink">Keyboard Shortcuts</h2>
        </div>

        {/* Navigation shortcuts */}
        <div className="mb-4">
          <h3 className="mb-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
            Navigation
          </h3>
          <div className="space-y-1.5">
            {SHORTCUTS.map((s) => (
              <div key={s.label} className="flex items-center justify-between py-1">
                <span className="text-sm text-ink">{s.description}</span>
                <span className="flex items-center gap-1">
                  {s.keys.map((k) => (
                    <kbd
                      key={k}
                      className={kbdClassName}
                    >
                      {k}
                    </kbd>
                  ))}
                </span>
              </div>
            ))}
          </div>
        </div>

        {/* Utility shortcuts */}
        <div className="mb-4">
          <h3 className="mb-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
            Utility
          </h3>
          <div className="space-y-1.5">
            <div className="flex items-center justify-between py-1">
              <span className="text-sm text-ink">Command palette</span>
              <span className="flex items-center gap-1">
                <kbd className={kbdClassName}>
                  Ctrl
                </kbd>
                <kbd className={kbdClassName}>
                  K
                </kbd>
              </span>
            </div>
            <div className="flex items-center justify-between py-1">
              <span className="text-sm text-ink">Toggle shortcuts help</span>
              <kbd className={kbdClassName}>
                ?
              </kbd>
            </div>
            <div className="flex items-center justify-between py-1">
              <span className="text-sm text-ink">Close overlay</span>
              <kbd className={kbdClassName}>
                Esc
              </kbd>
            </div>
          </div>
        </div>

        {/* Footer hint */}
        <p className="text-center text-xs text-muted-foreground">
          Press <kbd className="rounded bg-surface-2 px-1 py-0.5 font-mono text-xs text-ink">g</kbd> then a letter within 1 second
        </p>
      </div>
    </div>
  );
}
