import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export function Drawer({
  open,
  onClose,
  children,
}: {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
}) {
  if (!open) {
    return null;
  }
  return (
    <div className="fixed inset-0 z-40 lg:left-64">
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 bg-transparent"
      />
      <div
        className={cn(
          "absolute right-0 top-0 h-full w-full max-w-lg overflow-y-auto bg-white/95 p-6 shadow-2xl"
        )}
      >
        {children}
      </div>
    </div>
  );
}
