/*
 * EDGE-105 — MCPLane
 * A dedicated timeline lane that surfaces MCP-layer activity from an
 * Edge Session: mcp.server.connected/failed, mcp.tool.pre/post/failed,
 * and approval.* events that flow through the MCP gateway.
 *
 * The lane mounts BELOW the existing P0 hook timeline on
 * EdgeSessionDetailPage; it never reorders or replaces P0 lanes (task
 * rail #2). Chip-toggle filters persist via the `?mcp_lane=` URL query
 * so views are shareable. Defense-in-depth client redaction is enforced
 * by `lib/redaction.ts` inside the inline-expand MCPInspector body.
 */

import { useEffect, useMemo, useState, type ComponentType, type SVGProps } from "react";
import { useSearchParams } from "react-router-dom";
import { motion } from "framer-motion";
import {
  Plug,
  Unplug,
  Send,
  CornerDownLeft,
  AlertOctagon,
  Shield,
  ShieldCheck,
  ShieldOff,
  Cable,
  Clock,
  ChevronRight,
  ChevronDown,
} from "lucide-react";
import type { AgentActionEvent } from "@/api/types";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import { cn, formatRelativeTime } from "@/lib/utils";
import {
  useMcpLaneFiltersStore,
  parseMcpLaneFromUrl,
  serializeMcpLaneToUrl,
  type McpLaneChip,
} from "@/state/mcpLaneFilters";
import { MCPInspector } from "@/components/timeline/inspector/MCPInspector";

interface MCPLaneProps {
  events: AgentActionEvent[];
}

interface ChipDef {
  key: McpLaneChip;
  label: string;
}

const CHIPS: readonly ChipDef[] = [
  { key: "servers", label: "Servers" },
  { key: "tools", label: "Tools" },
  { key: "approvals", label: "Approvals" },
  { key: "failures", label: "Failures" },
] as const;

type IconComp = ComponentType<SVGProps<SVGSVGElement>>;

interface KindMeta {
  category: McpLaneChip;
  icon: IconComp;
  iconTestid: string;
  iconClassName: string;
  labelKind: string;
}

function classifyEvent(event: AgentActionEvent): KindMeta | null {
  const { kind, layer, status } = event;
  if (kind === "mcp.server.failed") {
    return {
      category: "failures",
      icon: Unplug,
      iconTestid: "mcp-icon-server-failed",
      iconClassName: "text-red-500",
      labelKind: "mcp.server.failed",
    };
  }
  if (kind === "mcp.server.connected") {
    return {
      category: "servers",
      icon: Plug,
      iconTestid: "mcp-icon-server-connected",
      iconClassName: "text-cyan-600 dark:text-cyan-300",
      labelKind: "mcp.server.connected",
    };
  }
  if (kind === "mcp.tool.failed" || (kind.startsWith("mcp.tool.") && status === "failed")) {
    return {
      category: "failures",
      icon: AlertOctagon,
      iconTestid: "mcp-icon-tool-failed",
      iconClassName: "text-red-500",
      labelKind: "mcp.tool.failed",
    };
  }
  if (kind === "mcp.tool.pre") {
    return {
      category: "tools",
      icon: Send,
      iconTestid: "mcp-icon-tool-pre",
      iconClassName: "text-cyan-600 dark:text-cyan-300",
      labelKind: "mcp.tool.pre",
    };
  }
  if (kind === "mcp.tool.post") {
    return {
      category: "tools",
      icon: CornerDownLeft,
      iconTestid: "mcp-icon-tool-post",
      iconClassName: "text-emerald-600 dark:text-emerald-300",
      labelKind: "mcp.tool.post",
    };
  }
  if (kind.startsWith("mcp.tool.")) {
    return {
      category: "tools",
      icon: Cable,
      iconTestid: "mcp-icon-tool-other",
      iconClassName: "text-cyan-600 dark:text-cyan-300",
      labelKind: kind,
    };
  }
  if (layer === "mcp" && kind.startsWith("approval.")) {
    if (kind === "approval.requested") {
      return {
        category: "approvals",
        icon: Shield,
        iconTestid: "mcp-icon-approval-required",
        iconClassName: "text-amber-500",
        labelKind: kind,
      };
    }
    if (kind === "approval.granted") {
      return {
        category: "approvals",
        icon: ShieldCheck,
        iconTestid: "mcp-icon-approval-consumed",
        iconClassName: "text-emerald-600 dark:text-emerald-300",
        labelKind: kind,
      };
    }
    if (kind === "approval.rejected") {
      return {
        category: "approvals",
        icon: ShieldOff,
        iconTestid: "mcp-icon-approval-rejected",
        iconClassName: "text-red-500",
        labelKind: kind,
      };
    }
  }
  return null;
}

const DECISION_TONE: Record<string, BadgeVariant> = {
  ALLOW: "healthy",
  DENY: "danger",
  REQUIRE_APPROVAL: "warning",
  REDACT: "warning",
  RECORDED: "info",
  THROTTLE: "warning",
  CONSTRAIN: "info",
};

function decisionVariant(decision: AgentActionEvent["decision"]): BadgeVariant {
  return DECISION_TONE[String(decision).toUpperCase()] ?? "info";
}

function useMcpLaneUrlSync() {
  const [searchParams, setSearchParams] = useSearchParams();
  const chips = useMcpLaneFiltersStore((s) => s.chips);
  const setChips = useMcpLaneFiltersStore((s) => s.setChips);
  const toggleStore = useMcpLaneFiltersStore((s) => s.toggle);

  // Hydrate from URL on mount (and whenever the param changes externally).
  useEffect(() => {
    const fromUrl = parseMcpLaneFromUrl(searchParams.get("mcp_lane"));
    setChips(fromUrl);
  }, []);

  const toggle = (key: McpLaneChip) => {
    toggleStore(key);
    const nextChips = useMcpLaneFiltersStore.getState().chips;
    const serialized = serializeMcpLaneToUrl(nextChips);
    const next = new URLSearchParams(searchParams);
    if (serialized === undefined) {
      next.delete("mcp_lane");
    } else {
      next.set("mcp_lane", serialized);
    }
    setSearchParams(next, { replace: true });
  };

  return { chips, toggle };
}

export function MCPLane({ events }: MCPLaneProps) {
  const { chips, toggle } = useMcpLaneUrlSync();
  const [expandedEventId, setExpandedEventId] = useState<string | null>(null);

  const classified = useMemo(() => {
    const items: { event: AgentActionEvent; meta: KindMeta }[] = [];
    for (const event of events) {
      const meta = classifyEvent(event);
      if (meta) items.push({ event, meta });
    }
    return items;
  }, [events]);

  const visible = useMemo(
    () => classified.filter((item) => chips.has(item.meta.category)),
    [classified, chips],
  );

  if (classified.length === 0) {
    return (
      <section
        className="rounded-3xl border border-dashed border-border bg-surface-1/50 p-6 text-center"
        data-testid="mcp-lane-empty"
      >
        <Plug className="mx-auto h-7 w-7 text-muted-foreground/40" aria-hidden="true" />
        <p className="mt-3 text-sm font-medium text-foreground">
          No MCP activity recorded for this session
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          This session did not invoke any MCP tools or connect to an upstream server.
        </p>
      </section>
    );
  }

  return (
    <section
      className="rounded-3xl border border-border bg-surface-1/70 p-4"
      data-testid="mcp-lane"
    >
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="text-xs font-medium uppercase tracking-[0.2em] text-cyan-600 dark:text-cyan-300">
            MCP lane
          </p>
          <h2 className="mt-1 text-lg font-semibold text-foreground">Upstream MCP activity</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {classified.length} event{classified.length === 1 ? "" : "s"}
            {visible.length !== classified.length ? ` · ${visible.length} after filter` : ""}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="MCP lane filters">
          {CHIPS.map((chip) => {
            const active = chips.has(chip.key);
            return (
              <button
                key={chip.key}
                type="button"
                onClick={() => toggle(chip.key)}
                data-testid={`mcp-chip-${chip.key}`}
                aria-pressed={active}
                className={cn(
                  "rounded-full border px-2.5 py-1 text-[10px] font-medium uppercase tracking-[0.14em] transition-colors duration-200",
                  active
                    ? "border-cyan-300/40 bg-cyan-500/15 text-cyan-700 dark:text-cyan-200"
                    : "border-border bg-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                {chip.label}
              </button>
            );
          })}
        </div>
      </header>

      {visible.length === 0 ? (
        <div
          className="mt-4 rounded-2xl border border-dashed border-border bg-surface-1/40 p-4 text-center text-xs text-muted-foreground"
          data-testid="mcp-lane-no-filters"
        >
          All MCP filters off — re-enable a chip above to see events.
        </div>
      ) : (
        <ol className="mt-4 space-y-2" data-testid="mcp-lane-list">
          {visible.map(({ event, meta }) => {
            const isExpanded = expandedEventId === event.eventId;
            const Icon = meta.icon;
            return (
              <motion.li
                key={event.eventId}
                layout
                initial={{ opacity: 0, y: 4 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ duration: 0.18 }}
                className="rounded-2xl border border-border bg-surface-1/80 shadow-soft transition-shadow hover:shadow-soft-hover"
              >
                <button
                  type="button"
                  onClick={() => setExpandedEventId((prev) => (prev === event.eventId ? null : event.eventId))}
                  aria-expanded={isExpanded}
                  data-testid={`mcp-row-${event.eventId}`}
                  className="flex w-full flex-wrap items-center justify-between gap-3 rounded-2xl px-3 py-2 text-left"
                >
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    {isExpanded ? (
                      <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />
                    ) : (
                      <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" aria-hidden="true" />
                    )}
                    <Icon
                      className={cn("h-4 w-4", meta.iconClassName)}
                      data-testid={meta.iconTestid}
                      aria-hidden="true"
                    />
                    <StatusBadge variant={decisionVariant(event.decision)}>
                      {String(event.decision)}
                    </StatusBadge>
                    <span className="font-mono text-xs text-foreground">{meta.labelKind}</span>
                    {event.toolName ? (
                      <span className="text-xs text-muted-foreground">· {event.toolName}</span>
                    ) : null}
                    {event.approvalRef ? (
                      <span className="font-mono text-[10px] text-cordum">{event.approvalRef}</span>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
                    <Clock className="h-3 w-3" aria-hidden="true" />
                    <span>{formatRelativeTime(event.ts)}</span>
                    <span className="font-mono">#{event.seq}</span>
                  </div>
                </button>
                {isExpanded ? <MCPInspector event={event} /> : null}
              </motion.li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
