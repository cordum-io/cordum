import { RotateCcw } from "lucide-react";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";

interface BundleRollbackDialogProps {
  open: boolean;
  snapshotId: string;
  loading: boolean;
  onClose: () => void;
  onConfirm: () => void;
}

export function BundleRollbackDialog({
  open,
  snapshotId,
  loading,
  onClose,
  onConfirm,
}: BundleRollbackDialogProps) {
  return (
    <ConfirmDialog
      open={open}
      onClose={onClose}
      onConfirm={onConfirm}
      title="Rollback policy to snapshot"
      description={
        <p>
          This will restore all policy bundles to the state captured in snapshot{" "}
          <code className="font-mono text-foreground bg-surface-2 px-1 py-0.5 rounded text-[10px]">
            {snapshotId}
          </code>
          . The current live policy will be replaced.
        </p>
      }
      confirmLabel="Rollback"
      variant="destructive"
      loading={loading}
      icon={RotateCcw}
    />
  );
}
