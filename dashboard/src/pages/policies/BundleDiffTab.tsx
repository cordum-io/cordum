import { GitCompare } from "lucide-react";
import { EmptyState } from "@/components/ui/EmptyState";

interface BundleDiffTabProps {
  bundleId: string;
}

/**
 * Bundle detail — Diff tab (Dashboard 5 step-8 placeholder).
 *
 * Step-8 ships the Monaco DiffEditor side-by-side comparison of two
 * versions' rule snapshots, plus a "X added, Y removed, Z modified"
 * summary row. Lazy-loaded so DiffEditor + monaco-yaml only load when
 * the user navigates to this tab. This placeholder keeps the tab route
 * + URL contract (`?tab=diff&from=A&to=B`) stable.
 */
export default function BundleDiffTab({ bundleId }: BundleDiffTabProps) {
  return (
    <EmptyState
      icon={<GitCompare className="h-5 w-5" />}
      title="Diff viewer coming online"
      description={`The Monaco read-only diff between two bundle versions ships with Dashboard 5 step-8. Bundle id: ${bundleId}.`}
    />
  );
}
