import { Link, useLocation } from "react-router-dom";
import { ChevronRight } from "lucide-react";
import { cn } from "../../lib/utils";

// ---------------------------------------------------------------------------
// Label maps
// ---------------------------------------------------------------------------

/** Top-level route segment -> display name */
const ROUTE_LABELS: Record<string, string> = {
  jobs: "Jobs",
  workflows: "Workflows",
  agents: "Agent Fleet",
  approvals: "Approvals",
  policies: "Policy Studio",
  packs: "Packs",
  dlq: "Dead Letters",
  audit: "Audit Log",
  settings: "Settings",
  search: "Search",
};

/** Nested sub-segments -> display name */
const SUB_LABELS: Record<string, string> = {
  rules: "Rules",
  new: "New",
  edit: "Edit",
  runs: "Runs",
  health: "System Health",
  keys: "API Keys",
  users: "Users",
  general: "General",
  sso: "SSO",
  notifications: "Notifications",
  environments: "Environments",
  maintenance: "Maintenance",
  setup: "Setup",
  diagnostics: "Diagnostics",
  history: "History",
  analytics: "Analytics",
  simulator: "Simulator",
};

/** Detect UUID-like hex strings (8+ hex chars) */
const UUID_RE = /^[0-9a-f]{8,}$/i;

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

interface Crumb {
  label: string;
  path: string;
  isId: boolean;
}

function buildCrumbs(pathname: string): Crumb[] {
  const segments = pathname.split("/").filter(Boolean);
  if (segments.length === 0) return [];

  const crumbs: Crumb[] = [];
  let accumulated = "";

  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i];
    accumulated += `/${seg}`;
    const isId = UUID_RE.test(seg);

    let label: string;
    if (i === 0) {
      label = ROUTE_LABELS[seg] ?? capitalize(seg);
    } else if (isId) {
      label = seg.slice(0, 8);
    } else {
      label = SUB_LABELS[seg] ?? capitalize(seg);
    }

    crumbs.push({ label, path: accumulated, isId });
  }

  return crumbs;
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

export function Breadcrumbs() {
  const location = useLocation();
  const crumbs = buildCrumbs(location.pathname);

  // Root path — just show "Overview"
  if (crumbs.length === 0) {
    return (
      <nav className="flex items-center gap-1 text-xs">
        <span className="font-semibold text-ink">Overview</span>
      </nav>
    );
  }

  return (
    <nav className="flex items-center gap-1 text-xs">
      <Link to="/" className="text-muted-foreground transition-colors hover:text-accent">
        Overview
      </Link>
      {crumbs.map((crumb, i) => {
        const isLast = i === crumbs.length - 1;
        return (
          <span key={crumb.path} className="flex items-center gap-1">
            <ChevronRight className="h-3 w-3 text-muted/50" />
            {isLast ? (
              <span
                className={cn(
                  "font-semibold text-ink",
                  crumb.isId && "font-mono text-[10px]",
                )}
              >
                {crumb.label}
              </span>
            ) : (
              <Link
                to={crumb.path}
                className={cn(
                  "text-muted-foreground transition-colors hover:text-accent",
                  crumb.isId && "font-mono text-[10px]",
                )}
              >
                {crumb.label}
              </Link>
            )}
          </span>
        );
      })}
    </nav>
  );
}
