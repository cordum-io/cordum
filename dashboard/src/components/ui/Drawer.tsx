import type { ReactNode } from "react";
import { cn } from "../../lib/utils";
import { useDialogA11y } from "../../hooks/useDialogA11y";

export function Drawer({
  open,
  onClose,
  children,
  size = "lg",
  label,
}: {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  size?: "sm" | "md" | "lg" | "xl" | "full";
  label?: string;
}) {
  const dialogRef = useDialogA11y(onClose);

  if (!open) {
    return null;
  }

  const sizeClass = {
    sm: "max-w-sm",
    md: "max-w-md",
    lg: "max-w-lg",
    xl: "max-w-xl",
    full: "max-w-full",
  }[size] || "max-w-lg";

  return (
    <div className="fixed inset-0 z-40 lg:left-64">
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 bg-black/20 backdrop-blur-sm animate-fade-in"
      />
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={label || "Drawer"}
        className={cn(
          "absolute right-0 top-0 h-full w-full overflow-y-auto bg-surface/95 p-6 shadow-2xl animate-slide-in border-l border-border",
          sizeClass
        )}
      >
        {children}
      </div>
    </div>
  );
}
