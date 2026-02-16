import { useId } from "react";
import { X } from "lucide-react";
import { Button } from "./Button";
import { useDialogA11y } from "../../hooks/useDialogA11y";

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  confirmVariant?: "primary" | "danger" | "outline" | "ghost";
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

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="surface-card w-full max-w-md rounded-3xl p-6 shadow-xl"
      >
        <div className="mb-4 flex items-center justify-between">
          <h3
            id={titleId}
            className="font-display text-lg font-semibold text-ink"
          >
            {title}
          </h3>
          <button
            type="button"
            onClick={onCancel}
            className="rounded-full p-1 hover:bg-surface2"
            aria-label="Close dialog"
          >
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        <p className="mb-6 text-sm text-muted">{message}</p>

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
