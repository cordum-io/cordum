import { useState } from "react";
import { Upload } from "lucide-react";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";

interface BundlePublishDialogProps {
  open: boolean;
  bundleName: string;
  loading: boolean;
  onClose: () => void;
  onConfirm: (note: string) => void;
}

export function BundlePublishDialog({
  open,
  bundleName,
  loading,
  onClose,
  onConfirm,
}: BundlePublishDialogProps) {
  const [note, setNote] = useState("");

  const handleConfirm = () => {
    onConfirm(note.trim());
    setNote("");
  };

  const handleClose = () => {
    if (loading) return;
    setNote("");
    onClose();
  };

  return (
    <ConfirmDialog
      open={open}
      onClose={handleClose}
      onConfirm={handleConfirm}
      title="Publish policy bundle"
      description={
        <div className="space-y-2">
          <p>
            You are about to publish <strong className="text-foreground">{bundleName}</strong>.
            This will make the current draft the live policy evaluated against all requests.
          </p>
          <label className="block">
            <span className="text-xs uppercase tracking-wider text-muted-foreground">
              Publish note (optional)
            </span>
            <input
              type="text"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="e.g. Added rate limit rules"
              className="mt-1 w-full h-8 px-3 text-xs bg-surface-0 border border-border rounded-xl text-foreground placeholder:text-muted-foreground/40 focus:outline-none focus:ring-1 focus:ring-cordum"
            />
          </label>
        </div>
      }
      confirmLabel="Publish"
      loading={loading}
      icon={Upload}
    />
  );
}

