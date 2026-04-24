import { createPortal } from "react-dom";
import { useEffect, useState, useCallback } from "react";
import { CheckCircle, XCircle, AlertTriangle, Info, X, type LucideIcon } from "lucide-react";
import { cn } from "../../lib/utils";
import { useToastStore, type Toast as ToastData, type ToastType } from "../../state/toast";

const variantConfig: Record<
  ToastType,
  { icon: LucideIcon; className: string }
> = {
  success: {
    icon: CheckCircle,
    className: "bg-[color:rgba(31,122,87,0.12)] border-l-4 border-l-success",
  },
  error: {
    icon: XCircle,
    className: "bg-[color:rgba(184,58,58,0.14)] border-l-4 border-l-danger",
  },
  warning: {
    icon: AlertTriangle,
    className: "bg-[color:rgba(197,138,28,0.18)] border-l-4 border-l-warning",
  },
  info: {
    icon: Info,
    className: "bg-[color:rgba(15,127,122,0.12)] border-l-4 border-l-accent",
  },
};

const iconColor: Record<ToastType, string> = {
  success: "text-success",
  error: "text-danger",
  warning: "text-warning",
  info: "text-accent",
};

export function Toast({ toast }: { toast: ToastData }) {
  const dismissToast = useToastStore((s) => s.dismissToast);
  const [exiting, setExiting] = useState(false);

  const handleDismiss = useCallback(() => {
    setExiting(true);
    setTimeout(() => dismissToast(toast.id), 250);
  }, [dismissToast, toast.id]);

  useEffect(() => {
    const timer = setTimeout(handleDismiss, toast.duration);
    return () => clearTimeout(timer);
  }, [toast.duration, handleDismiss]);

  const config = variantConfig[toast.type];
  const Icon = config.icon;

  return (
    <div
      className={cn(
        "relative flex gap-3 p-4 rounded-xl shadow-lg min-w-[320px] max-w-[420px] text-ink",
        config.className,
        exiting ? "animate-slide-out" : "animate-slide-in"
      )}
      role="alert"
    >
      <Icon size={20} className={cn("shrink-0 mt-0.5", iconColor[toast.type])} />
      <div className="flex-1 min-w-0">
        <p className="font-semibold text-sm">{toast.title}</p>
        {toast.description && (
          <p className="text-xs text-muted-foreground mt-1">{toast.description}</p>
        )}
      </div>
      {toast.dismissible && (
        <button type="button"
          onClick={handleDismiss}
          className="absolute top-2 right-2 p-1 rounded-xl text-muted-foreground hover:text-ink transition-colors"
        >
          <X size={14} />
        </button>
      )}
    </div>
  );
}

export function ToastContainer() {
  const toasts = useToastStore((s) => s.toasts);

  if (toasts.length === 0) return null;

  return createPortal(
    <div className="fixed bottom-4 right-4 z-50 flex flex-col-reverse gap-2">
      {toasts.map((t) => (
        <Toast key={t.id} toast={t} />
      ))}
    </div>,
    document.body
  );
}
