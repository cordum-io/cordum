import type { ReactNode } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { useDialogA11y } from "@/hooks/useDialogA11y";
import { useMotionConfig } from "@/hooks/useMotionConfig";

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
  /** Optional selector for the element that should receive initial focus */
  initialFocusSelector?: string;
}

/**
 * Reusable modal overlay with:
 * - Focus trapping (Tab/Shift+Tab cycle)
 * - Escape key closes
 * - role="dialog", aria-modal="true", aria-label
 * - Backdrop with blur
 * - Framer Motion enter/exit (respects prefers-reduced-motion)
 */
export function DialogOverlay({
  open,
  onClose,
  children,
  label,
  className,
  backdropClose = true,
  initialFocusSelector,
}: DialogOverlayProps) {
  const dialogRef = useDialogA11y(onClose, {
    enabled: open,
    initialFocusSelector,
  });
  const { fadeIn, scaleIn } = useMotionConfig();

  return (
    <AnimatePresence>
      {open && (
        <>
          <motion.div
            {...fadeIn}
            className="fixed inset-0 z-[100] bg-[color:var(--surface-glass)] backdrop-blur-md"
            onClick={backdropClose ? onClose : undefined}
          />
          <motion.div
            {...scaleIn}
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
