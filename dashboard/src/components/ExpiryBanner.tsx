import { AlertTriangle, ShieldAlert } from "lucide-react";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { cn } from "@/lib/utils";

interface ExpiryBannerProps {
  status?: string | null;
  expiresAt?: string | null;
  className?: string;
}

function formatDate(raw?: string | null): string | null {
  if (!raw) return null;
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return raw;
  return parsed.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

function formatRelativeWindow(raw?: string | null): string | null {
  if (!raw) return null;
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) return null;

  const diffMs = parsed.getTime() - Date.now();
  const dayMs = 24 * 60 * 60 * 1000;
  const days = Math.ceil(Math.abs(diffMs) / dayMs);

  if (diffMs >= 0) {
    return days <= 1 ? "less than a day remaining" : `${days} days remaining`;
  }
  return days <= 1 ? "expired less than a day ago" : `expired ${days} days ago`;
}

export function ExpiryBanner({ status, expiresAt, className }: ExpiryBannerProps) {
  const normalized = (status ?? "").trim().toLowerCase();
  const dateLabel = formatDate(expiresAt);
  const relativeLabel = formatRelativeWindow(expiresAt);

  if (normalized === "warning") {
    return (
      <InfoBanner
        variant="warning"
        title="License renewal window is open"
        icon={<AlertTriangle className="h-3.5 w-3.5" />}
        className={cn(className)}
      >
        {dateLabel
          ? `This license expires on ${dateLabel}${relativeLabel ? ` (${relativeLabel})` : ""}. Renew now to avoid falling back to Community limits.`
          : "This license is approaching expiry. Renew now to avoid falling back to Community limits."}
      </InfoBanner>
    );
  }

  if (normalized === "grace") {
    return (
      <InfoBanner
        variant="warning"
        title="License grace period active"
        icon={<ShieldAlert className="h-3.5 w-3.5" />}
        className={cn(className)}
      >
        {dateLabel
          ? `This license expired on ${dateLabel}${relativeLabel ? ` (${relativeLabel})` : ""}. Cordum is still honoring the grace window, but renewal should be completed immediately.`
          : "This license has expired and Cordum is honoring the grace window. Renew immediately to avoid Community fallback."}
      </InfoBanner>
    );
  }

  if (normalized === "degraded") {
    return (
      <InfoBanner
        variant="error"
        title="Enterprise features are degraded"
        icon={<ShieldAlert className="h-3.5 w-3.5" />}
        className={cn(className)}
      >
        {dateLabel
          ? `This license expired on ${dateLabel}. Cordum is now enforcing Community-tier limits while preserving audit visibility and break-glass access.`
          : "This license has expired. Cordum is now enforcing Community-tier limits while preserving audit visibility and break-glass access."}
      </InfoBanner>
    );
  }

  return null;
}
