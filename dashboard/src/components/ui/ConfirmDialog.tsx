import { useId } from "react";
import { X } from "lucide-react";
import { Button } from "./Button";
import { useDialogA11y } from "../../hooks/useDialogA11y";

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  confirmVariant?: "primary" | "destructive" | "outline" | "ghost";
  isPending?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = "Confirm",
  confirmVariant = "primary",
  isPending = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const dialogRef = useDialogA11y(onCancel);
  const titleId = useId();
  const descriptionId = useId();

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-surface-0/80 p-4 backdrop-blur-sm">
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
        className="w-full max-w-md rounded-xl border border-border bg-surface-1 p-6 shadow-lift"
      >
        <div className="mb-4 flex items-center justify-between">
          <h3
            id={titleId}
            className="font-display text-lg font-semibold text-foreground"
          >
            {title}
          </h3>
          <button
            type="button"
            onClick={onCancel}
            className="rounded-full p-1 transition-colors duration-micro hover:bg-surface-2/70 focus-visible:ring-2 focus-visible:ring-accent/35"
            aria-label="Close dialog"
          >
            <X className="h-4 w-4 text-muted-foreground" />
          </button>
        </div>

        <p id={descriptionId} className="mb-6 text-sm text-muted-foreground">{message}</p>

        <div className="flex justify-end gap-3">
          <Button
            variant="ghost"
            size="sm"
            type="button"
            onClick={onCancel}
            disabled={isPending}
          >
            Cancel
          </Button>
          <Button
            variant={confirmVariant}
            size="sm"
            type="button"
            onClick={onConfirm}
            disabled={isPending}
          >
            {isPending ? "Working..." : confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
