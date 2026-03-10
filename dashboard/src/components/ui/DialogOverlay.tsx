import type { ReactNode } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { useDialogA11y } from "@/hooks/useDialogA11y";

interface DialogOverlayProps {
  open: boolean;
  onClose: () => void;
  children: ReactNode;
  /** aria-label for the dialog (required for screen readers) */
  label: string;
  /** Additional class names for the content wrapper */
  className?: string;
  /** Whether clicking the backdrop closes the dialog (default: true) */
  backdropClose?: boolean;
}

/**
 * Reusable modal overlay with:
 * - Focus trapping (Tab/Shift+Tab cycle)
 * - Escape key closes
 * - role="dialog", aria-modal="true", aria-label
 * - Backdrop with blur
 * - Framer Motion enter/exit
 */
export function DialogOverlay({
  open,
  onClose,
  children,
  label,
  className,
  backdropClose = true,
}: DialogOverlayProps) {
  const dialogRef = useDialogA11y(onClose);

  return (
    <AnimatePresence>
      {open && (
        <>
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            className="fixed inset-0 z-[100] bg-[color:var(--surface-glass)] backdrop-blur-md"
            onClick={backdropClose ? onClose : undefined}
          />
          <motion.div
            initial={{ opacity: 0, scale: 0.96, y: 8 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.96, y: 8 }}
            transition={{ duration: 0.15, ease: "easeOut" }}
            className="fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-[101] w-full max-w-md"
          >
            <div
              ref={dialogRef}
              role="dialog"
              aria-modal="true"
              aria-label={label}
              className={className}
            >
              {children}
            </div>
          </motion.div>
        </>
      )}
    </AnimatePresence>
  );
}
