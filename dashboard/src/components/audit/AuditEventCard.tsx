import { Link, useNavigate } from "react-router-dom";
import { LogIn, LogOut, ArrowRight, Link2 } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { HighlightText } from "../ui/HighlightText";
import { cn } from "../../lib/utils";
import type { AuditEntry } from "../../api/types";

// ---------------------------------------------------------------------------
// Category classification
// ---------------------------------------------------------------------------

export type AuditCategory =
  | "safety_decision"
  | "human_action"
  | "system_event"
  | "access_event";

const SAFETY_ACTIONS = new Set([
  "allow", "deny", "require_approval", "throttle",
  "safety_allow", "safety_deny", "safety_throttle",
  "safety_require_approval", "evaluate",
]);

const ACCESS_ACTIONS = new Set([
  "login", "logout", "auth_failure", "token_refresh",
  "api_key_created", "api_key_revoked",
]);

const HUMAN_RESOURCE_TYPES = new Set([
  "policy", "bundle", "workflow", "pack", "config",
  "user", "role", "approval",
]);

export function classifyEvent(entry: AuditEntry): AuditCategory {
  const action = (entry.action || entry.eventType || "").toLowerCase();
  if (SAFETY_ACTIONS.has(action)) return "safety_decision";
  if (ACCESS_ACTIONS.has(action)) return "access_event";
  if (HUMAN_RESOURCE_TYPES.has(entry.resourceType?.toLowerCase()))
    return "human_action";
  return "system_event";
}

// ---------------------------------------------------------------------------
// Severity classification
// ---------------------------------------------------------------------------

type Severity = "high" | "medium" | "low";

const HIGH_ACTIONS = new Set([
  "deny", "safety_deny", "require_approval",
  "safety_require_approval", "auth_failure",
]);

function classifySeverity(entry: AuditEntry): Severity {
  const action = (entry.action || entry.eventType || "").toLowerCase();
  if (HIGH_ACTIONS.has(action)) return "high";
  if (
    entry.resourceType === "policy" ||
    entry.resourceType === "approval" ||
    action.includes("approve") ||
    action.includes("reject")
  )
    return "medium";
  return "low";
}

// ---------------------------------------------------------------------------
// Category styling
// ---------------------------------------------------------------------------

const categoryBorder: Record<AuditCategory, string> = {
  safety_decision: "border-l-blue-500",
  human_action: "border-l-purple-500",
  system_event: "border-l-gray-400",
  access_event: "border-l-amber-500",
};

const severityDot: Record<Severity, string> = {
  high: "bg-red-500",
  medium: "bg-yellow-500",
  low: "",
};

// ---------------------------------------------------------------------------
// Timestamp formatting (millisecond precision)
// ---------------------------------------------------------------------------

function formatTimestampMs(iso?: string): string {
  if (!iso) return "\u2014";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const pad = (n: number, w = 2) => String(n).padStart(w, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
}

// ---------------------------------------------------------------------------
// Resource link helper
// ---------------------------------------------------------------------------

function resourceLink(resourceType: string, resourceId: string): string | null {
  switch (resourceType?.toLowerCase()) {
    case "job":
      return `/jobs/${resourceId}`;
    case "workflow":
      return `/workflows/${resourceId}`;
    case "policy":
    case "bundle":
      return `/policies`;
    case "approval":
      return `/approvals`;
    default:
      return null;
  }
}

// ---------------------------------------------------------------------------
// Decision badge color
// ---------------------------------------------------------------------------

const decisionVariant: Record<string, string> = {
  allow: "success",
  safety_allow: "success",
  deny: "danger",
  safety_deny: "danger",
  require_approval: "warning",
  safety_require_approval: "warning",
  throttle: "info",
  safety_throttle: "info",
  evaluate: "info",
};

// ---------------------------------------------------------------------------
// Category-specific content renderers
// ---------------------------------------------------------------------------

function SafetyDecisionContent({ entry, searchQuery }: { entry: AuditEntry; searchQuery?: string }) {
  const action = (entry.action || entry.eventType || "").toLowerCase();
  const variant = (decisionVariant[action] ?? "default") as "success" | "warning" | "danger" | "info" | "default";
  const riskTags = Array.isArray(entry.payload?.risk_tags)
    ? (entry.payload.risk_tags as string[])
    : [];
  const link = resourceLink(entry.resourceType, entry.resourceId);

  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2">
        <Badge variant={variant}>
          {(entry.action || entry.eventType).toUpperCase()}
        </Badge>
        {link ? (
          <Link to={link} className="text-sm font-medium text-accent hover:underline">
            <HighlightText text={`${entry.resourceType}:${entry.resourceId.slice(0, 12)}`} query={searchQuery ?? ""} />
          </Link>
        ) : (
          <span className="text-sm font-medium text-ink">
            <HighlightText text={`${entry.resourceType}:${entry.resourceId.slice(0, 12)}`} query={searchQuery ?? ""} />
          </span>
        )}
      </div>
      {entry.message && (
        <p className="text-xs text-muted"><HighlightText text={entry.message} query={searchQuery ?? ""} /></p>
      )}
      {riskTags.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {riskTags.map((t) => (
            <Badge key={t} variant="warning">{t}</Badge>
          ))}
        </div>
      )}
    </div>
  );
}

function HumanActionContent({ entry, searchQuery }: { entry: AuditEntry; searchQuery?: string }) {
  const link = resourceLink(entry.resourceType, entry.resourceId);
  const hasDiff =
    entry.payload?.snapshot_before != null || entry.payload?.snapshot_after != null;

  return (
    <div className="space-y-1.5">
      <p className="text-sm">
        <span className="font-semibold text-ink"><HighlightText text={`@${entry.actor}`} query={searchQuery ?? ""} /></span>{" "}
        <span className="font-medium text-ink"><HighlightText text={entry.action} query={searchQuery ?? ""} /></span>{" "}
        {link ? (
          <Link to={link} className="text-accent hover:underline">
            <HighlightText text={`${entry.resourceType}:${entry.resourceId.slice(0, 12)}`} query={searchQuery ?? ""} />
          </Link>
        ) : (
          <span className="text-muted">
            <HighlightText text={`${entry.resourceType}:${entry.resourceId.slice(0, 12)}`} query={searchQuery ?? ""} />
          </span>
        )}
      </p>
      {entry.message && (
        <p className="text-xs text-muted"><HighlightText text={entry.message} query={searchQuery ?? ""} /></p>
      )}
      {hasDiff && (
        <p className="text-xs text-muted italic">
          Content modified (click to view diff)
        </p>
      )}
    </div>
  );
}

function SystemEventContent({ entry, searchQuery }: { entry: AuditEntry; searchQuery?: string }) {
  const link = resourceLink(entry.resourceType, entry.resourceId);

  return (
    <div className="space-y-1 opacity-80">
      <div className="flex items-center gap-2 text-sm text-muted">
        {link ? (
          <Link to={link} className="text-accent hover:underline">
            <HighlightText text={`${entry.resourceType}:${entry.resourceId.slice(0, 12)}`} query={searchQuery ?? ""} />
          </Link>
        ) : (
          <span><HighlightText text={`${entry.resourceType}:${entry.resourceId.slice(0, 12)}`} query={searchQuery ?? ""} /></span>
        )}
        <ArrowRight className="h-3 w-3" />
        <Badge variant="default">{entry.action || entry.eventType}</Badge>
      </div>
      {entry.message && (
        <p className="text-xs text-muted"><HighlightText text={entry.message} query={searchQuery ?? ""} /></p>
      )}
    </div>
  );
}

function AccessEventContent({ entry, searchQuery }: { entry: AuditEntry; searchQuery?: string }) {
  const action = (entry.action || entry.eventType || "").toLowerCase();
  const isFailure = action.includes("failure") || action.includes("denied");
  const isLogout = action.includes("logout");

  return (
    <div className="space-y-1.5">
      <div className="flex items-center gap-2 text-sm">
        {isLogout ? (
          <LogOut className="h-4 w-4 text-muted" />
        ) : (
          <LogIn className={cn("h-4 w-4", isFailure ? "text-danger" : "text-success")} />
        )}
        <span className="font-semibold text-ink"><HighlightText text={entry.actor} query={searchQuery ?? ""} /></span>
        <Badge variant={isFailure ? "danger" : "success"}>
          {entry.action || entry.eventType}
        </Badge>
      </div>
      {entry.message && (
        <p className="text-xs text-muted"><HighlightText text={entry.message} query={searchQuery ?? ""} /></p>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Payload-only match detection
// ---------------------------------------------------------------------------

function isPayloadOnlyMatch(entry: AuditEntry, query?: string): boolean {
  if (!query?.trim()) return false;
  const lower = query.toLowerCase();
  const visibleHit =
    entry.action.toLowerCase().includes(lower) ||
    entry.actor.toLowerCase().includes(lower) ||
    entry.message.toLowerCase().includes(lower) ||
    entry.resourceType.toLowerCase().includes(lower) ||
    entry.resourceId.toLowerCase().includes(lower);
  if (visibleHit) return false;
  return !!(entry.payload && JSON.stringify(entry.payload).toLowerCase().includes(lower));
}

// ---------------------------------------------------------------------------
// AuditEventCard
// ---------------------------------------------------------------------------

interface AuditEventCardProps {
  entry: AuditEntry;
  onClick: (id: string) => void;
  searchQuery?: string;
}

export function AuditEventCard({ entry, onClick, searchQuery }: AuditEventCardProps) {
  const navigate = useNavigate();
  const category = classifyEvent(entry);
  const severity = classifySeverity(entry);

  function renderContent() {
    switch (category) {
      case "safety_decision":
        return <SafetyDecisionContent entry={entry} searchQuery={searchQuery} />;
      case "human_action":
        return <HumanActionContent entry={entry} searchQuery={searchQuery} />;
      case "system_event":
        return <SystemEventContent entry={entry} searchQuery={searchQuery} />;
      case "access_event":
        return <AccessEventContent entry={entry} searchQuery={searchQuery} />;
    }
  }

  function handleRelated(e: React.MouseEvent) {
    e.stopPropagation();
    navigate(`/audit?resource=${entry.resourceType}:${entry.resourceId}&view=correlation`);
  }

  return (
    <Card
      className={cn(
        "border-l-4 cursor-pointer transition-shadow hover:shadow-lift",
        categoryBorder[category],
      )}
      onClick={() => onClick(entry.id)}
    >
      <div className="space-y-2">
        {/* Header: timestamp + severity dot */}
        <div className="flex items-center justify-between">
          <span className="font-mono text-[11px] text-muted">
            {formatTimestampMs(entry.timestamp)}
          </span>
          {severity !== "low" && (
            <span
              className={cn("h-2 w-2 rounded-full", severityDot[severity])}
              title={`${severity} severity`}
            />
          )}
        </div>

        {/* Category-specific content */}
        {renderContent()}

        {/* Payload-only match indicator */}
        {isPayloadOnlyMatch(entry, searchQuery) && (
          <p className="text-[11px] italic text-muted">Match found in payload</p>
        )}

        {/* Related events action */}
        {entry.resourceId && (
          <button
            type="button"
            onClick={handleRelated}
            className="flex items-center gap-1 text-[11px] text-muted hover:text-accent transition-colors"
          >
            <Link2 className="h-3 w-3" />
            Related
          </button>
        )}
      </div>
    </Card>
  );
}
