import { useMemo } from "react";
import { usePolicyBundle } from "@/hooks/usePolicies";
import { SkeletonCard } from "@/components/ui/Skeleton";

interface BundleDiffViewProps {
  bundleId: string;
  draftYaml: string;
}

interface DiffLine {
  type: "unchanged" | "added" | "removed";
  content: string;
  lineNum: number;
}

function computeDiff(published: string, draft: string): DiffLine[] {
  const pubLines = published.split(/\r?\n/);
  const draftLines = draft.split(/\r?\n/);
  const result: DiffLine[] = [];
  const maxLen = Math.max(pubLines.length, draftLines.length);

  for (let i = 0; i < maxLen; i++) {
    const pub = i < pubLines.length ? pubLines[i] : undefined;
    const drft = i < draftLines.length ? draftLines[i] : undefined;

    if (pub === drft) {
      result.push({ type: "unchanged", content: pub ?? "", lineNum: i + 1 });
    } else {
      if (pub !== undefined) {
        result.push({ type: "removed", content: pub, lineNum: i + 1 });
      }
      if (drft !== undefined) {
        result.push({ type: "added", content: drft, lineNum: i + 1 });
      }
    }
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
      <div className="rounded-lg border border-border bg-surface-1 p-4 text-xs text-muted-foreground">
        No differences between draft and published content.
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
        draft vs published
      </p>
      <div className="instrument-card overflow-auto max-h-[520px] p-0">
        <pre className="p-3 text-xs font-mono leading-relaxed">
          {diffLines.map((line, i) => (
            <div key={i} className={LINE_STYLES[line.type]}>
              <span className="inline-block w-5 text-right mr-2 text-muted-foreground/40 select-none">
                {LINE_PREFIX[line.type]}
              </span>
              {line.content}
            </div>
          ))}
        </pre>
      </div>
    </div>
  );
}
