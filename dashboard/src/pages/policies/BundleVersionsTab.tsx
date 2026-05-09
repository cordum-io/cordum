import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Clock, Copy, GitBranch } from "lucide-react";
import { EmptyState } from "@/components/ui/EmptyState";
import { Select } from "@/components/ui/Select";
import { formatRelativeTime } from "@/lib/utils";
import { useBundleVersions } from "@/hooks/useBundle";
import type { BundleVersion } from "@/api/generated/model/bundleVersion";

interface BundleVersionsTabProps {
  bundleId: string;
}

/**
 * Bundle detail — Versions tab (Dashboard 5 step 4d).
 *
 * Renders a vertical timeline of bundle versions newest-first. Each row
 * shows version label + deployed_at relative time + truncated audit
 * hash (with copy-on-click) + "Compare with…" picker that navigates to
 * the Diff tab with `?tab=diff&from=<this>&to=<other>`.
 *
 * Versions are immutable post-deploy (per spec L96) — no edit affordance
 * is rendered.
 */
export default function BundleVersionsTab({ bundleId }: BundleVersionsTabProps) {
  const { data, isPending } = useBundleVersions(bundleId);
  const versions = data?.items ?? [];

  // Sort newest-first by deployed_at; defensive copy so callers' arrays
  // aren't mutated.
  const sorted = [...versions].sort((a, b) =>
    b.deployed_at.localeCompare(a.deployed_at),
  );

  if (isPending) {
    return (
      <div className="text-sm text-muted-foreground py-6 text-center">
        Loading versions…
      </div>
    );
  }

  if (sorted.length === 0) {
    return (
      <EmptyState
        icon={<GitBranch className="h-5 w-5" />}
        title="No versions yet"
        description="Versions appear here when the bundle is deployed to a scope. Each deploy creates an immutable snapshot for audit + rollback."
      />
    );
  }

  return (
    <ol className="space-y-3" aria-label="Bundle versions newest first">
      {sorted.map((version, idx) => (
        <VersionRow
          key={version.version}
          version={version}
          allVersions={sorted}
          isLatest={idx === 0}
        />
      ))}
    </ol>
  );
}

function VersionRow({
  version,
  allVersions,
  isLatest,
}: {
  version: BundleVersion;
  allVersions: BundleVersion[];
  isLatest: boolean;
}) {
  const navigate = useNavigate();
  const [copied, setCopied] = useState(false);

  const auditHashShort = version.audit_hash
    ? version.audit_hash.slice(0, 8)
    : null;
  const ruleCount = version.rule_snapshot?.length ?? 0;

  const onCopy = async () => {
    if (!version.audit_hash) return;
    try {
      await navigator.clipboard.writeText(version.audit_hash);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API may be unavailable (e.g. older browsers / sandboxed
      // contexts). Silent fall-through is fine — the truncated hash is
      // still rendered for manual copy.
    }
  };

  return (
    <li className="rounded-2xl border border-border bg-surface-1 p-4 flex flex-wrap items-center gap-x-4 gap-y-2">
      <div className="flex items-center gap-2 min-w-0 flex-1">
        <span className="font-mono text-sm font-semibold text-foreground">
          {version.version}
        </span>
        {isLatest && (
          <span className="text-[10px] font-mono uppercase tracking-wider rounded-full px-2 py-0.5 bg-cordum/12 text-cordum">
            Latest
          </span>
        )}
        <span className="text-xs text-muted-foreground inline-flex items-center gap-1">
          <Clock className="h-3 w-3" aria-hidden />
          {formatRelativeTime(version.deployed_at)}
        </span>
        <span className="text-xs text-muted-foreground tabular-nums">
          {ruleCount} {ruleCount === 1 ? "rule" : "rules"}
        </span>
      </div>

      {auditHashShort && (
        <button
          type="button"
          onClick={onCopy}
          className="inline-flex items-center gap-1 text-xs font-mono text-muted-foreground hover:text-cordum transition-colors"
          aria-label={`Copy audit hash for version ${version.version}`}
        >
          <Copy className="h-3 w-3" aria-hidden />
          {copied ? "Copied" : auditHashShort}
        </button>
      )}

      {allVersions.length > 1 && (
        <CompareWithPicker
          current={version.version}
          others={allVersions
            .map((v) => v.version)
            .filter((v) => v !== version.version)}
          onPick={(other) => {
            navigate(
              `?tab=diff&from=${encodeURIComponent(version.version)}&to=${encodeURIComponent(other)}`,
              { replace: false },
            );
          }}
        />
      )}
    </li>
  );
}

function CompareWithPicker({
  current,
  others,
  onPick,
}: {
  current: string;
  others: string[];
  onPick: (other: string) => void;
}) {
  return (
    <label className="inline-flex items-center gap-2 text-xs">
      <span className="text-muted-foreground">Compare with</span>
      <Select
        defaultValue=""
        onChange={(e) => {
          const next = (e.target as HTMLSelectElement).value;
          if (next) onPick(next);
        }}
        aria-label={`Compare version ${current} with another`}
        className="h-7 text-xs max-w-[160px]"
        options={[
          { value: "", label: "…select" },
          ...others.map((other) => ({ value: other, label: other })),
        ]}
      />
    </label>
  );
}
