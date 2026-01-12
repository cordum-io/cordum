import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export function Drawer({
  open,
  onClose,
  children,
  size = "lg",
}: {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  size?: "sm" | "md" | "lg" | "xl" | "full";
}) {
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
        className="absolute inset-0 bg-transparent"
      />
      <div
        className={cn(
          "absolute right-0 top-0 h-full w-full overflow-y-auto bg-white/95 p-6 shadow-2xl",
          sizeClass
        )}
      >
        {children}
      </div>
    </div>
  );
}