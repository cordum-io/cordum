import { Link } from "react-router-dom";
import { CheckCircle, Circle, X } from "lucide-react";
import { Drawer } from "../ui/Drawer";
import { Badge } from "../ui/Badge";
import { Button } from "../ui/Button";
import { ProgressBar } from "../ProgressBar";
import type { ChecklistItem } from "../../hooks/useSetupStatus";

// ---------------------------------------------------------------------------
// SetupChecklist
// ---------------------------------------------------------------------------

interface SetupChecklistProps {
  open: boolean;
  onClose: () => void;
  items: ChecklistItem[];
  completedCount: number;
  totalRequired: number;
  onDismissForever: () => void;
}

export function SetupChecklist({
  open,
  onClose,
  items,
  completedCount,
  totalRequired,
  onDismissForever,
}: SetupChecklistProps) {
  const percentage = totalRequired > 0 ? Math.round((completedCount / totalRequired) * 100) : 0;
  const requiredItems = items.filter((i) => !i.optional);
  const optionalItems = items.filter((i) => i.optional);

  return (
    <Drawer open={open} onClose={onClose} size="md">
      <div className="space-y-5">
        {/* Header */}
        <div className="flex items-start justify-between">
          <div>
            <h2 className="font-display text-lg font-semibold text-ink">
              Setup Guide
            </h2>
            <p className="mt-0.5 text-xs text-muted">
              Complete these steps to get your Cordum instance ready.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-full p-1.5 hover:bg-surface2"
          >
            <X className="h-4 w-4 text-muted" />
          </button>
        </div>

        {/* Progress */}
        <div className="space-y-1.5">
          <div className="flex items-center justify-between text-xs">
            <span className="font-medium text-ink">
              {completedCount} of {totalRequired} complete
            </span>
            <span className="text-muted">{percentage}%</span>
          </div>
          <ProgressBar value={percentage} />
        </div>

        {/* Required items */}
        <div className="space-y-1">
          {requiredItems.map((item) => (
            <ChecklistRow key={item.id} item={item} onNavigate={onClose} />
          ))}
        </div>

        {/* Optional items */}
        {optionalItems.length > 0 && (
          <div className="space-y-1">
            <p className="text-xs font-semibold uppercase tracking-wide text-muted">
              Optional
            </p>
            {optionalItems.map((item) => (
              <ChecklistRow key={item.id} item={item} onNavigate={onClose} />
            ))}
          </div>
        )}

        {/* Dismiss forever */}
        <div className="border-t border-border pt-4">
          <Button
            variant="ghost"
            size="sm"
            className="w-full"
            onClick={() => {
              onDismissForever();
              onClose();
            }}
          >
            Dismiss forever
          </Button>
          <p className="mt-1 text-center text-[10px] text-muted">
            You can re-open this guide from the Settings sidebar.
          </p>
        </div>
      </div>
    </Drawer>
  );
}

// ---------------------------------------------------------------------------
// ChecklistRow
// ---------------------------------------------------------------------------

function ChecklistRow({
  item,
  onNavigate,
}: {
  item: ChecklistItem;
  onNavigate: () => void;
}) {
  return (
    <Link
      to={item.route}
      onClick={onNavigate}
      className="flex items-center gap-3 rounded-xl px-3 py-2.5 transition-colors hover:bg-surface2/50"
    >
      {item.completed ? (
        <CheckCircle className="h-5 w-5 shrink-0 text-success" />
      ) : (
        <Circle className="h-5 w-5 shrink-0 text-muted" />
      )}
      <span
        className={
          item.completed
            ? "text-sm text-muted line-through"
            : "text-sm font-medium text-ink"
        }
      >
        {item.label}
      </span>
      {item.optional && (
        <Badge variant="default" className="ml-auto text-[10px]">
          Optional
        </Badge>
      )}
    </Link>
  );
}
