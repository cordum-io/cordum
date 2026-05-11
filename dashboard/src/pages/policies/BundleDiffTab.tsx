import { Suspense, lazy, useMemo, type ComponentType } from "react";
import { useQueryState, parseAsString } from "nuqs";
import { stringify as yamlStringify } from "yaml";
import { GitCompare } from "lucide-react";
import { EmptyState } from "@/components/ui/EmptyState";
import { Select } from "@/components/ui/Select";
import { useBundleVersions } from "@/hooks/useBundle";
import { useBundleVersion } from "@/hooks/useBundleVersion";
import type { Rule } from "@/api/generated/model/rule";

// Hand-typed minimal slice of @monaco-editor/react's DiffEditorProps
// (typeof import("@monaco-editor/react").DiffEditorProps loses through the
// dynamic-import lazy() boundary). Keep aligned if we ever pass more options.
interface MonacoDiffEditorProps {
  original: string;
  modified: string;
  language?: string;
  height?: string | number;
  options?: Record<string, unknown>;
}

const DiffEditor = lazy(() =>
  import("@monaco-editor/react").then((m) => ({ default: m.DiffEditor })),
) as unknown as ComponentType<MonacoDiffEditorProps>;

interface BundleDiffTabProps {
  bundleId: string;
}

interface DiffSummary {
  added: number;
  removed: number;
  modified: number;
}

function indexById(rules: Rule[] | undefined): Map<string, Rule> {
  const m = new Map<string, Rule>();
  for (const r of rules ?? []) m.set(r.id, r);
  return m;
}

function computeSummary(from: Rule[] | undefined, to: Rule[] | undefined): DiffSummary {
  const a = indexById(from);
  const b = indexById(to);
  let added = 0;
  let removed = 0;
  let modified = 0;
  for (const [id, rule] of b) {
    const prev = a.get(id);
    if (!prev) {
      added += 1;
    } else if (JSON.stringify(prev) !== JSON.stringify(rule)) {
      modified += 1;
    }
  }
  for (const id of a.keys()) {
    if (!b.has(id)) removed += 1;
  }
  return { added, removed, modified };
}

/**
 * Bundle detail — Diff tab (Dashboard 5 step 8).
 * Read-only Monaco DiffEditor between two version snapshots' rules,
 * with a summary row above (added / removed / modified). Picks
 * versions via URL state (`?from=X&to=Y`) for deep-link compatibility
 * with Versions tab's "Compare with…" picker. DiffEditor is lazy-loaded
 * so monaco-editor only ships when the tab is activated.
 */
export default function BundleDiffTab({ bundleId }: BundleDiffTabProps) {
  const [from, setFrom] = useQueryState("from", parseAsString.withDefault(""));
  const [to, setTo] = useQueryState("to", parseAsString.withDefault(""));

  const versionsQ = useBundleVersions(bundleId);
  const versions = versionsQ.data?.items ?? [];

  const fromQ = useBundleVersion(bundleId, from);
  const toQ = useBundleVersion(bundleId, to);

  const summary = useMemo(
    () => computeSummary(fromQ.data?.rule_snapshot, toQ.data?.rule_snapshot),
    [fromQ.data?.rule_snapshot, toQ.data?.rule_snapshot],
  );

  const yamlOriginal = useMemo(
    () =>
      fromQ.data?.rule_snapshot
        ? yamlStringify(fromQ.data.rule_snapshot)
        : "",
    [fromQ.data?.rule_snapshot],
  );
  const yamlModified = useMemo(
    () =>
      toQ.data?.rule_snapshot ? yamlStringify(toQ.data.rule_snapshot) : "",
    [toQ.data?.rule_snapshot],
  );

  const versionOptions = useMemo(
    () =>
      versions.map((v) => ({ value: v.version, label: v.version })),
    [versions],
  );

  if (!from || !to) {
    return (
      <div className="space-y-4">
        <EmptyState
          icon={<GitCompare className="h-5 w-5" />}
          title="Pick two versions to compare"
          description="Select a base and a target version to view their rule-snapshot diff."
        />
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="block text-sm">
            <span className="mb-1 block text-muted-foreground">From version</span>
            <Select
              aria-label="From version"
              value={from}
              onChange={(e) => void setFrom(e.target.value)}
              options={versionOptions}
              placeholder="Select base version"
            />
          </label>
          <label className="block text-sm">
            <span className="mb-1 block text-muted-foreground">To version</span>
            <Select
              aria-label="To version"
              value={to}
              onChange={(e) => void setTo(e.target.value)}
              options={versionOptions}
              placeholder="Select target version"
            />
          </label>
        </div>
      </div>
    );
  }

  if (fromQ.isLoading || toQ.isLoading) {
    return <div className="text-sm text-muted-foreground">Loading versions…</div>;
  }

  return (
    <div className="space-y-3">
      <div
        role="status"
        aria-label="Diff summary"
        className="flex items-center gap-4 rounded-2xl border border-border bg-surface-1 px-4 py-2 text-sm"
      >
        <span className="font-medium text-foreground">{from} → {to}</span>
        <span className="text-success">{summary.added} added</span>
        <span className="text-danger">{summary.removed} removed</span>
        <span className="text-warning">{summary.modified} modified</span>
      </div>
      <div className="overflow-hidden rounded-2xl border border-border">
        <Suspense
          fallback={
            <div className="px-4 py-6 text-sm text-muted-foreground">
              Loading diff editor…
            </div>
          }
        >
          <DiffEditor
            original={yamlOriginal}
            modified={yamlModified}
            language="yaml"
            height="60vh"
            options={{
              readOnly: true,
              renderSideBySide: true,
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
            }}
          />
        </Suspense>
      </div>
    </div>
  );
}
