// NOTE: BundleDiffView uses raw <pre> intentionally for colored diff rendering.
// CodeBlock is for plain text display — diff views need per-line styling that
// CodeBlock doesn't support.
import { useMemo } from "react";
import { usePolicyBundle } from "@/hooks/usePolicies";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";

interface BundleDiffViewProps {
  bundleId: string;
  draftYaml: string;
}

interface DiffLine {
  type: "unchanged" | "added" | "removed";
  content: string;
  lineNum: number;
}

// LCS-based diff: builds the longest common subsequence of (published, draft)
// lines, then walks the LCS matrix to emit removed/added/unchanged entries
// in source order. This is correct under arbitrary insertions and deletions —
// an index-by-index compare would label every line after a single insertion
// as "changed", which is misleading for policy reviewers.
function computeDiff(published: string, draft: string): DiffLine[] {
  const pubLines = published.split(/\r?\n/);
  const draftLines = draft.split(/\r?\n/);
  const m = pubLines.length;
  const n = draftLines.length;

  // lcs[i][j] = length of LCS of pubLines[i..] and draftLines[j..]
  const lcs: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      if (pubLines[i] === draftLines[j]) {
        lcs[i][j] = lcs[i + 1][j + 1] + 1;
      } else {
        lcs[i][j] = Math.max(lcs[i + 1][j], lcs[i][j + 1]);
      }
    }
  }

  const result: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < m && j < n) {
    if (pubLines[i] === draftLines[j]) {
      result.push({ type: "unchanged", content: pubLines[i], lineNum: i + 1 });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      result.push({ type: "removed", content: pubLines[i], lineNum: i + 1 });
      i++;
    } else {
      result.push({ type: "added", content: draftLines[j], lineNum: j + 1 });
      j++;
    }
  }
  while (i < m) {
    result.push({ type: "removed", content: pubLines[i], lineNum: i + 1 });
    i++;
  }
  while (j < n) {
    result.push({ type: "added", content: draftLines[j], lineNum: j + 1 });
    j++;
  }
  return result;
}

const LINE_STYLES: Record<DiffLine["type"], string> = {
  unchanged: "text-muted-foreground",
  added: "bg-[var(--color-success)]/10 text-[var(--color-success)]",
  removed: "bg-destructive/10 text-destructive line-through",
};

const LINE_PREFIX: Record<DiffLine["type"], string> = {
  unchanged: " ",
  added: "+",
  removed: "-",
};

export function BundleDiffView({ bundleId, draftYaml }: BundleDiffViewProps) {
  const { data: liveBundle, isLoading } = usePolicyBundle(bundleId);
  const liveContent = liveBundle?.content ?? "";

  const diffLines = useMemo(
    () => computeDiff(liveContent, draftYaml),
    [liveContent, draftYaml],
  );

  const hasChanges = diffLines.some((l) => l.type !== "unchanged");

  if (isLoading) {
    return <SkeletonCard />;
  }

  if (!hasChanges) {
    return (
      <div className="rounded-xl border border-border bg-surface-1 p-4 text-xs text-muted-foreground">
        No differences between draft and published content.
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-xs font-mono uppercase tracking-wider text-muted-foreground">
        draft vs published
      </p>
      <div className="instrument-card overflow-auto max-h-[520px] p-0">
        {/* `<pre>` only allows phrasing-content children, so each diff row is
            a `<span style="display:block">` instead of a `<div>` to keep the
            HTML valid. */}
        <pre className="p-3 text-xs font-mono leading-relaxed">
          {diffLines.map((line, i) => (
            <span key={i} className={cn("block", LINE_STYLES[line.type])}>
              <span className="inline-block w-5 text-right mr-2 text-muted-foreground/40 select-none">
                {LINE_PREFIX[line.type]}
              </span>
              {line.content}
            </span>
          ))}
        </pre>
      </div>
    </div>
  );
}

