import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Size variants
// ---------------------------------------------------------------------------

const sizes = {
  sm: "h-4 w-4",
  md: "h-6 w-6",
  lg: "h-10 w-10",
} as const;

// ---------------------------------------------------------------------------
// Spinner
// ---------------------------------------------------------------------------

export function Spinner({
  size = "md",
  label,
  className,
}: {
  size?: keyof typeof sizes;
  label?: string;
  className?: string;
}) {
  const a11yProps = label
    ? { role: "status" as const, "aria-live": "polite" as const, "aria-label": label, "aria-hidden": undefined }
    : { "aria-hidden": true as const };

  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      className={cn("animate-spin", sizes[size], className)}
      {...a11yProps}
    >
      <circle
        cx="12"
        cy="12"
        r="10"
        stroke="currentColor"
        strokeWidth="3"
        opacity="0.2"
      />
      <path
        d="M12 2a10 10 0 0 1 10 10"
        stroke="currentColor"
        strokeWidth="3"
        strokeLinecap="round"
      />
    </svg>
  );
}

// ---------------------------------------------------------------------------
// LoadingScreen — full-area centered spinner with optional label
// ---------------------------------------------------------------------------

export function LoadingScreen({
  label = "Loading...",
  className,
}: {
  label?: string;
  className?: string;
}) {
  return (
    <div className={cn("flex min-h-[200px] items-center justify-center", className)}>
      <div className="flex flex-col items-center gap-3">
        <Spinner size="lg" label={label} />
        <p className="text-sm text-muted-foreground">{label}</p>
      </div>
    </div>
  );
}
