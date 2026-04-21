import {
  EyeOff,
  Gauge,
  Globe,
  Hash,
  User,
  type LucideIcon,
} from "lucide-react";
import type { PolicyConstraints } from "@/api/types";
import { useState } from "react";
import { Button } from "@/components/ui/Button";
import { CodeBlock } from "@/components/ui/CodeBlock";

interface ConstraintRow {
  key: string;
  label: string;
  value: string;
  icon: LucideIcon;
}

// safeStringify wraps JSON.stringify so we never crash the governance
// detail view on a malformed constraint payload (circular ref, BigInt,
// accidental function). Constraints come from the gateway, which we trust,
// but defence-in-depth on user-visible JSON rendering costs nothing.
function safeStringify(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return "// unrenderable constraint payload — see raw audit event";
  }
}

interface ConstraintsPanelProps {
  constraints?: PolicyConstraints;
}

function formatRateLimit(rateLimit: PolicyConstraints["rateLimit"]): string | null {
  if (rateLimit == null) return null;
  if (typeof rateLimit === "number") return `${rateLimit} req/window`;
  if (typeof rateLimit === "string") return rateLimit;
  if (typeof rateLimit === "object") {
    const limit = rateLimit.limit ?? rateLimit.requests;
    const windowSeconds = rateLimit.windowSeconds ?? rateLimit.window_seconds;
    const burst = rateLimit.burst;
    const parts = [
      typeof limit === "number" ? `${limit} requests` : null,
      typeof windowSeconds === "number" ? `${windowSeconds}s window` : null,
      typeof burst === "number" ? `burst ${burst}` : null,
    ].filter((value): value is string => Boolean(value));
    return parts.length > 0 ? parts.join(" · ") : JSON.stringify(rateLimit);
  }
  return null;
}

function formatRequireReviewer(
  value: PolicyConstraints["requireReviewer"],
): string | null {
  if (value == null) return null;
  if (typeof value === "boolean") return value ? "Reviewer required" : "No reviewer";
  if (typeof value === "string") return value;
  if (typeof value === "object") {
    return value.role ?? value.approverRole ?? value.reason ?? JSON.stringify(value);
  }
  return null;
}

function knownConstraintRows(constraints: PolicyConstraints): ConstraintRow[] {
  const rows: ConstraintRow[] = [];

  if (typeof constraints.maxInvocations === "number") {
    rows.push({
      key: "maxInvocations",
      label: "Max invocations",
      value: `${constraints.maxInvocations}`,
      icon: Hash,
    });
  }

  if (Array.isArray(constraints.allowedDomains) && constraints.allowedDomains.length > 0) {
    rows.push({
      key: "allowedDomains",
      label: "Allowed domains",
      value: constraints.allowedDomains.join(", "),
      icon: Globe,
    });
  }

  if (Array.isArray(constraints.maskedFields) && constraints.maskedFields.length > 0) {
    rows.push({
      key: "maskedFields",
      label: "Masked fields",
      value: constraints.maskedFields.join(", "),
      icon: EyeOff,
    });
  }

  const rateLimit = formatRateLimit(constraints.rateLimit);
  if (rateLimit) {
    rows.push({
      key: "rateLimit",
      label: "Rate limit",
      value: rateLimit,
      icon: Gauge,
    });
  }

  const reviewer = formatRequireReviewer(constraints.requireReviewer);
  if (reviewer) {
    rows.push({
      key: "requireReviewer",
      label: "Reviewer",
      value: reviewer,
      icon: User,
    });
  }

  return rows;
}

export function ConstraintsPanel({ constraints }: ConstraintsPanelProps) {
  if (!constraints || Object.keys(constraints).length === 0) {
    return null;
  }

  const [showAll, setShowAll] = useState(false);
  const rows = knownConstraintRows(constraints);
  const unknownEntries = Object.fromEntries(
    Object.entries(constraints).filter(
      ([key]) =>
        ![
          "maxInvocations",
          "allowedDomains",
          "maskedFields",
          "rateLimit",
          "requireReviewer",
        ].includes(key),
    ),
  );
  const hasUnknownEntries = Object.keys(unknownEntries).length > 0;

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs font-mono uppercase tracking-wider text-muted-foreground">
          Constraints
        </p>
        <span className="text-[10px] font-mono text-muted-foreground">
          Snapshot
        </span>
      </div>
      {rows.length > 0 && (
        <div className="space-y-2">
          {rows.map((row) => {
            const Icon = row.icon;
            return (
              <div
                key={row.key}
                className="surface-inset flex items-start gap-3 rounded-2xl px-3 py-2.5"
              >
                <span className="mt-0.5 rounded-full bg-surface-1 p-2 text-muted-foreground">
                  <Icon className="h-3.5 w-3.5" />
                </span>
                <div className="min-w-0">
                  <p className="text-xs font-mono uppercase tracking-wider text-muted-foreground">
                    {row.label}
                  </p>
                  <p
                    className={
                      showAll
                        ? "text-sm leading-relaxed text-foreground whitespace-normal break-words"
                        : "text-sm leading-relaxed text-foreground truncate"
                    }
                    title={showAll ? undefined : row.value}
                  >
                    {row.value}
                  </p>
                </div>
              </div>
            );
          })}
        </div>
      )}
      {hasUnknownEntries && (
        <CodeBlock
          title="Additional constraints"
          language="json"
          copyable={false}
          maxHeight={220}
        >
          {safeStringify(unknownEntries)}
        </CodeBlock>
      )}
      {(rows.length > 1 || hasUnknownEntries) && (
        <div className="flex justify-end">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setShowAll((value) => !value)}
          >
            {showAll ? "Show less" : "Show more"}
          </Button>
        </div>
      )}
    </div>
  );
}
