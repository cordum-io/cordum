import { useState, useEffect } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { X, Command } from "lucide-react";

const shortcuts = [
  { section: "Global", items: [
    { keys: ["⌘", "K"], description: "Open command palette" },
    { keys: ["⌘", "/"], description: "Toggle sidebar" },
    { keys: ["Esc"], description: "Close modal / dialog / popover" },
    { keys: ["?"], description: "Show keyboard shortcuts" },
  ]},
  { section: "Lists", items: [
    { keys: ["j"], description: "Move down in list" },
    { keys: ["k"], description: "Move up in list" },
    { keys: ["Enter"], description: "Open selected item" },
  ]},
  { section: "Navigation", items: [
    { keys: ["g", "h"], description: "Go to Home" },
    { keys: ["g", "j"], description: "Go to Jobs" },
    { keys: ["g", "a"], description: "Go to Agents" },
    { keys: ["g", "w"], description: "Go to Workflows" },
    { keys: ["g", "p"], description: "Go to Policies" },
  ]},
];

export function KeyboardShortcutsDialog() {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      // Only trigger on "?" when not in an input
      const target = e.target as HTMLElement;
      const isInput = target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable;
      if (e.key === "?" && !isInput) {
        e.preventDefault();
        setOpen((prev) => !prev);
      }
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  return (
    <AnimatePresence>
      {open && (
        <>
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="fixed inset-0 z-[100] bg-black/60 backdrop-blur-sm"
            onClick={() => setOpen(false)}
          />
          <motion.div
            initial={{ opacity: 0, scale: 0.96 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.96 }}
            role="dialog"
            aria-modal="true"
            aria-labelledby="keyboard-shortcuts-title"
            className="fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-[101] w-full max-w-lg"
          >
            <div className="bg-surface-1 border border-border rounded-xl shadow-2xl overflow-hidden">
              <div className="flex items-center justify-between px-5 py-4 border-b border-border">
                <h3
                  id="keyboard-shortcuts-title"
                  className="text-sm font-display font-semibold text-foreground flex items-center gap-2"
                >
                  <Command className="w-4 h-4 text-cordum" />
                  Keyboard Shortcuts
                </h3>
                <button type="button"
                  aria-label="Close keyboard shortcuts dialog"
                  onClick={() => setOpen(false)}
                  className="p-1 rounded-xl hover:bg-surface-2 text-muted-foreground hover:text-foreground transition-colors"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>
              <div className="p-5 space-y-5 max-h-[60vh] overflow-y-auto">
                {shortcuts.map((section) => (
                  <div key={section.section}>
                    <p className="text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground/60 mb-2">
                      {section.section}
                    </p>
                    <div className="space-y-1.5">
                      {section.items.map((item) => (
                        <div key={item.description} className="flex items-center justify-between py-1.5">
                          <span className="text-xs text-foreground">{item.description}</span>
                          <div className="flex items-center gap-1">
                            {item.keys.map((key, i) => (
                              <span key={i}>
                                <kbd className="inline-flex items-center justify-center min-w-[22px] h-[22px] px-1.5 text-xs font-mono rounded bg-surface-2 border border-border text-muted-foreground">
                                  {key}
                                </kbd>
                                {i < item.keys.length - 1 && (
                                  <span className="text-muted-foreground/40 mx-0.5 text-xs">+</span>
                                )}
                              </span>
                            ))}
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  );
}

