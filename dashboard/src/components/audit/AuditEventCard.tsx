import { Link, useNavigate } from "react-router-dom";
import { LogIn, LogOut, ArrowRight, Link2 } from "lucide-react";
import { Badge } from "../ui/Badge";
import { Card } from "../ui/Card";
import { HighlightText } from "../ui/HighlightText";
import { cn } from "../../lib/utils";
import { useState } from "react";
import type { AuditEntry } from "../../api/types";
import { AlertSeverity } from "../../api/types";

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
  safety_decision: "border-l-[var(--color-info)]",
  human_action: "border-l-primary",
  system_event: "border-l-muted",
  access_event: "border-l-[var(--color-warning)]",
};

const severityDot: Record<Severity, string> = {
  high: "bg-destructive",
  medium: "bg-[var(--color-warning)]",
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
// Resource display helpers
// ---------------------------------------------------------------------------

function resourceLabel(entry: AuditEntry): string {
  if (entry.resourceName) return entry.resourceName;
  if (!entry.resourceId) return entry.resourceType || "unknown";
  return `${entry.resourceType}:${entry.resourceId.slice(0, 12)}`;
}

function resourceLink(resourceType: string, resourceId: string): string | null {
  switch (resourceType?.toLowerCase()) {
    case "job":
      return `/jobs/${resourceId}`;
    case "workflow":
      return `/workflows/${resourceId}/studio`;
    case "policy":
      return `/govern/overview`;
    case "bundle":
      return `/govern/overview?tab=bundles`;
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
  deny: "governance",
  safety_deny: "governance",
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
            <HighlightText text={resourceLabel(entry)} query={searchQuery ?? ""} />
          </Link>
        ) : (
          <span className="text-sm font-medium text-ink">
            <HighlightText text={resourceLabel(entry)} query={searchQuery ?? ""} />
          </span>
        )}
        {entry.resourceName && entry.resourceId && (
          <span className="text-xs text-muted-foreground">{entry.resourceId.slice(0, 12)}</span>
        )}
      </div>
      {entry.message && (
        <p className="text-xs text-muted-foreground"><HighlightText text={entry.message} query={searchQuery ?? ""} /></p>
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
            <HighlightText text={resourceLabel(entry)} query={searchQuery ?? ""} />
          </Link>
        ) : (
          <span className="text-muted-foreground">
            <HighlightText text={resourceLabel(entry)} query={searchQuery ?? ""} />
          </span>
        )}
      </p>
      {entry.message && (
        <p className="text-xs text-muted-foreground"><HighlightText text={entry.message} query={searchQuery ?? ""} /></p>
      )}
      {hasDiff && (
        <p className="text-xs text-muted-foreground italic">
          Content modified (click to view diff)
        </p>
      )}
    </div>
  );
}

const alertSeverityVariant: Record<number, "info" | "warning" | "danger" | "default"> = {
  [AlertSeverity.INFO]: "info",
  [AlertSeverity.WARNING]: "warning",
  [AlertSeverity.ERROR]: "danger",
  [AlertSeverity.CRITICAL]: "danger",
};

const alertSeverityLabel: Record<number, string> = {
  [AlertSeverity.INFO]: "INFO",
  [AlertSeverity.WARNING]: "WARNING",
  [AlertSeverity.ERROR]: "ERROR",
  [AlertSeverity.CRITICAL]: "CRITICAL",
};

function SystemEventContent({ entry, searchQuery }: { entry: AuditEntry; searchQuery?: string }) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const link = resourceLink(entry.resourceType, entry.resourceId);
  const severity = typeof entry.payload?.severity === "number" ? entry.payload.severity as number : undefined;
  const sourceComponent = typeof entry.payload?.source_component === "string" ? entry.payload.source_component as string : undefined;
  const traceId = typeof entry.payload?.trace_id === "string" ? entry.payload.trace_id as string : undefined;
  const details = entry.payload?.details != null && typeof entry.payload.details === "object"
    ? entry.payload.details as Record<string, unknown>
    : undefined;

  return (
    <div className="space-y-1.5 opacity-80">
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        {link ? (
          <Link to={link} className="text-accent hover:underline">
            <HighlightText text={resourceLabel(entry)} query={searchQuery ?? ""} />
          </Link>
        ) : (
          <span><HighlightText text={resourceLabel(entry)} query={searchQuery ?? ""} /></span>
        )}
        <ArrowRight className="h-3 w-3" />
        <Badge variant="default">{entry.action || entry.eventType}</Badge>
        {severity != null && severity !== AlertSeverity.UNSPECIFIED && (
          <Badge
            variant={alertSeverityVariant[severity] ?? "default"}
            className={severity === AlertSeverity.CRITICAL ? "font-bold" : undefined}
          >
            {alertSeverityLabel[severity] ?? `SEV-${severity}`}
          </Badge>
        )}
        {sourceComponent && (
          <span className="inline-flex items-center rounded border border-border/60 bg-surface2 px-1.5 py-0.5 text-xs font-mono text-muted-foreground">
            {sourceComponent}
          </span>
        )}
      </div>
      {entry.message && (
        <p className="text-xs text-muted-foreground"><HighlightText text={entry.message} query={searchQuery ?? ""} /></p>
      )}
      {traceId && (
        <p className="text-xs">
          <span className="text-muted-foreground">Trace: </span>
          <Link to={`/jobs/${traceId}`} className="font-mono text-accent hover:underline">
            {traceId.slice(0, 12)}...
          </Link>
        </p>
      )}
      {details && Object.keys(details).length > 0 && (
        <div>
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); setDetailsOpen(!detailsOpen); }}
            className="text-xs text-muted-foreground hover:text-accent transition-colors"
          >
            {detailsOpen ? "Hide details" : "Show details"}
          </button>
          {detailsOpen && (
            <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
              {Object.entries(details).map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="font-semibold text-muted-foreground">{k}</dt>
                  <dd className="font-mono text-ink truncate">{String(v)}</dd>
                </div>
              ))}
            </dl>
          )}
        </div>
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
          <LogOut className="h-4 w-4 text-muted-foreground" />
        ) : (
          <LogIn className={cn("h-4 w-4", isFailure ? "text-danger" : "text-success")} />
        )}
        <span className="font-semibold text-ink"><HighlightText text={entry.actor} query={searchQuery ?? ""} /></span>
        <Badge variant={isFailure ? "danger" : "success"}>
          {entry.action || entry.eventType}
        </Badge>
      </div>
      {entry.message && (
        <p className="text-xs text-muted-foreground"><HighlightText text={entry.message} query={searchQuery ?? ""} /></p>
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
          <span className="font-mono text-xs text-muted-foreground">
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
          <p className="text-xs italic text-muted-foreground">Match found in payload</p>
        )}

        {/* Related events action */}
        {entry.resourceId && (
          <button
            type="button"
            onClick={handleRelated}
            className="flex items-center gap-1 text-xs text-muted-foreground hover:text-accent transition-colors"
          >
            <Link2 className="h-3 w-3" />
            Related
          </button>
        )}
      </div>
    </Card>
  );
}
