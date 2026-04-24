import { useState, type ReactNode } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { AlertTriangle, X } from "lucide-react";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import { useMotionConfig } from "@/hooks/useMotionConfig";

export interface ConfirmDialogProps {
  open: boolean;
  onClose?: () => void;
  onCancel?: () => void;
  onConfirm: () => void;
  title: string;
  description?: string | ReactNode;
  message?: string | ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  variant?: "default" | "destructive";
  confirmVariant?: string;
  confirmText?: string; // If set, user must type this to confirm
  loading?: boolean;
  isPending?: boolean;
  icon?: React.ElementType;
  initialFocusSelector?: string;
}

export function ConfirmDialog({
  open,
  onClose,
  onCancel,
  onConfirm,
  title,
  description,
  message,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  variant = "default",
  confirmVariant: _confirmVariant,
  confirmText,
  loading,
  isPending,
  icon: Icon = AlertTriangle,
  initialFocusSelector,
}: ConfirmDialogProps) {
  const resolvedDescription = description ?? message ?? "";
  const resolvedLoading = loading ?? isPending ?? false;
  const resolvedOnClose = onClose ?? onCancel ?? (() => {});
  const [typed, setTyped] = useState("");
  const { fadeIn, scaleIn } = useMotionConfig();
  const canConfirm = confirmText ? typed === confirmText : true;

  const handleConfirm = () => {
    if (!canConfirm || resolvedLoading) return;
    onConfirm();
    setTyped("");
  };

  const handleClose = () => {
    if (resolvedLoading) return;
    setTyped("");
    resolvedOnClose();
  };

  const dialogRef = useDialogA11y(handleClose, {
    enabled: open,
    initialFocusSelector,
  });

  return (
    <AnimatePresence>
      {open && (
        <>
          <motion.div
            {...fadeIn}
            className="fixed inset-0 z-[100] bg-[color:var(--surface-glass)] backdrop-blur-md"
            onClick={handleClose}
          />
          <motion.div
            {...scaleIn}
            className="fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-[101] w-full max-w-md"
          >
            <div
              ref={dialogRef}
              role="dialog"
              aria-modal="true"
              aria-labelledby="confirm-dialog-title"
              aria-describedby="confirm-dialog-desc"
              className="overflow-hidden rounded-3xl border border-border bg-[color:var(--surface-glass)] shadow-glow backdrop-blur-xl"
            >
              {/* Header */}
              <div className="flex items-start gap-3 px-5 pt-5 pb-3">
                <div className={`shrink-0 w-10 h-10 rounded-xl flex items-center justify-center ${
                  variant === "destructive" ? "bg-destructive/15" : "bg-[var(--color-warning)]/15"
                }`}>
                  <Icon className={`w-5 h-5 ${
                    variant === "destructive" ? "text-destructive" : "text-[var(--color-warning)]"
                  }`} />
                </div>
                <div className="flex-1 min-w-0">
                  <h3 id="confirm-dialog-title" className="text-sm font-display font-semibold text-foreground">{title}</h3>
                  <div id="confirm-dialog-desc" className="text-xs text-muted-foreground mt-1 leading-relaxed">{resolvedDescription}</div>
                </div>
                <button type="button"
                  onClick={handleClose}
                  aria-label="Close dialog"
                  className="shrink-0 p-1 rounded-xl hover:bg-surface-2 text-muted-foreground hover:text-foreground transition-colors"
                >
                  <X className="w-4 h-4" />
                </button>
              </div>

              {/* Confirm text input */}
              {confirmText && (
                <div className="px-5 pb-3">
                  <p className="text-xs text-muted-foreground mb-1.5">
                    Type <code className="font-mono text-foreground bg-surface-2 px-1 py-0.5 rounded text-xs">{confirmText}</code> to confirm
                  </p>
                  <input
                    type="text"
                    value={typed}
                    onChange={(e) => setTyped(e.target.value)}
                    placeholder={confirmText}
                    className="w-full h-9 px-3 text-xs bg-surface-0 border border-border rounded-2xl text-foreground placeholder:text-muted-foreground/40 focus:outline-none focus:ring-1 focus:ring-cordum font-mono"
                  />
                </div>
              )}

              {/* Actions */}
              <div className="flex items-center justify-end gap-2 px-5 py-3 border-t border-border bg-surface-0/50">
                <button type="button"
                  onClick={handleClose}
                  disabled={resolvedLoading}
                  className="h-8 px-4 text-xs font-medium rounded-full border border-border text-foreground hover:bg-surface-2 transition-colors disabled:opacity-50"
                >
                  {cancelLabel}
                </button>
                <button type="button"
                  onClick={handleConfirm}
                  disabled={!canConfirm || resolvedLoading}
                  className={`h-8 px-4 text-xs font-medium rounded-full transition-colors disabled:opacity-50 disabled:cursor-not-allowed ${
                    variant === "destructive"
                      ? "bg-destructive text-foreground hover:bg-destructive/80"
                      : "bg-cordum text-surface-0 hover:bg-cordum-dim"
                  }`}
                >
                  {resolvedLoading ? (
                    <span className="flex items-center gap-1.5">
                      <svg className="w-3 h-3 animate-spin" viewBox="0 0 24 24" fill="none">
                        <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" strokeLinecap="round" className="opacity-25" />
                        <path d="M4 12a8 8 0 018-8" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
                      </svg>
                      Processing...
                    </span>
                  ) : confirmLabel}
                </button>
              </div>
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  );
}

