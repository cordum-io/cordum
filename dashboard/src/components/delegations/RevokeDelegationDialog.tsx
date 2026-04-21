import { ConfirmDialog } from "@/components/ui/ConfirmDialog";

export interface RevokeDelegationDialogProps {
  open: boolean;
  cascadeCount?: number;
  isCounting?: boolean;
  isPending?: boolean;
  onClose: () => void;
  onConfirm: () => void;
}

export function RevokeDelegationDialog({
  open,
  cascadeCount = 0,
  isCounting = false,
  isPending = false,
  onClose,
  onConfirm,
}: RevokeDelegationDialogProps) {
  const description = isCounting
    ? "Loading cascade impact…"
    : `Revoking this will cascade to ${cascadeCount} downstream delegation${cascadeCount === 1 ? "" : "s"}. Proceed?`;

  return (
    <ConfirmDialog
      open={open}
      onClose={onClose}
      onCancel={onClose}
      onConfirm={onConfirm}
      title="Revoke delegation"
      description={description}
      confirmLabel="Revoke delegation"
      cancelLabel="Keep token"
      variant="destructive"
      isPending={isPending}
    />
  );
}
