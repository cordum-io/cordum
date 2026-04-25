import { useMemo } from "react";
import { SafetyDecisionBadge } from "@/components/ui/SafetyDecisionBadge";
import type { PolicyBundle } from "@/api/types";

interface ByTopicTableProps {
  bundles: PolicyBundle[];
  filterText?: string;
}

interface TopicEntry {
  topic: string;
  bundleName: string;
  bundleId: string;
  ruleName: string;
  ruleId: string;
  decision: string;
  riskTags: string[];
  capabilities: string[];
}

export function ByTopicTable({ bundles, filterText }: ByTopicTableProps) {
  const entries = useMemo(() => {
    const result: TopicEntry[] = [];
    for (const b of bundles) {
      for (const r of b.rules ?? []) {
        const topics = r.match?.topics ?? ["*"];
        for (const topic of topics) {
          result.push({
            topic,
            bundleName: b.name,
            bundleId: b.id,
            ruleName: r.name,
            ruleId: r.id,
            decision: r.decision || "—",
            riskTags: r.match?.risk_tags ?? [],
            capabilities: r.match?.capabilities ?? [],
          });
        }
      }
    }
    // Sort by topic pattern
    result.sort((a, b) => a.topic.localeCompare(b.topic));
    return result;
  }, [bundles]);

  const filteredEntries = useMemo(() => {
    if (!filterText) return entries;
    const lower = filterText.toLowerCase();
    return entries.filter(
      (e) =>
        e.topic.toLowerCase().includes(lower) ||
        e.bundleName.toLowerCase().includes(lower) ||
        e.ruleName.toLowerCase().includes(lower) ||
        e.decision.toLowerCase().includes(lower),
    );
  }, [entries, filterText]);

  // Group by topic for visual grouping
  const grouped = useMemo(() => {
    const map = new Map<string, TopicEntry[]>();
    for (const e of filteredEntries) {
      const existing = map.get(e.topic) ?? [];
      existing.push(e);
      map.set(e.topic, existing);
    }
    return map;
  }, [filteredEntries]);

  return (
    <div className="overflow-hidden rounded-2xl border border-border bg-card">
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-xs">
          <thead>
            <tr>
              <th className="px-3 py-2.5 text-left font-mono text-xs uppercase tracking-widest text-muted-foreground bg-surface-2 rounded-tl-2xl">
                Topic Pattern
              </th>
              <th className="px-3 py-2.5 text-left font-mono text-xs uppercase tracking-widest text-muted-foreground bg-surface-2">
                Bundle
              </th>
              <th className="px-3 py-2.5 text-left font-mono text-xs uppercase tracking-widest text-muted-foreground bg-surface-2">
                Rule
              </th>
              <th className="px-3 py-2.5 text-left font-mono text-xs uppercase tracking-widest text-muted-foreground bg-surface-2">
                Decision
              </th>
              <th className="px-3 py-2.5 text-left font-mono text-xs uppercase tracking-widest text-muted-foreground bg-surface-2">
                Risk Tags
              </th>
              <th className="px-3 py-2.5 text-left font-mono text-xs uppercase tracking-widest text-muted-foreground bg-surface-2 rounded-tr-2xl">
                Capabilities
              </th>
            </tr>
          </thead>
          <tbody>
            {Array.from(grouped.entries()).map(([topic, entries]) =>
              entries.map((entry, i) => (
                <tr
                  key={`${entry.bundleId}:${entry.ruleId}:${topic}:${i}`}
                  className="border-b border-border/30 last:border-b-0 hover:bg-cordum/[0.03] transition-colors"
                >
                  {i === 0 ? (
                    <td
                      className="px-3 py-2.5 font-mono text-foreground font-medium align-top"
                      rowSpan={entries.length}
                    >
                      {topic}
                    </td>
                  ) : null}
                  <td className="px-3 py-2.5 font-mono text-cordum">
                    {entry.bundleName}
                  </td>
                  <td className="px-3 py-2.5 font-mono text-foreground">
                    {entry.ruleName}
                  </td>
                  <td className="px-3 py-2.5">
                    <SafetyDecisionBadge decision={entry.decision} />
                  </td>
                  <td className="px-3 py-2.5 font-mono text-muted-foreground">
                    {entry.riskTags.length > 0 ? entry.riskTags.join(", ") : "—"}
                  </td>
                  <td className="px-3 py-2.5 font-mono text-muted-foreground">
                    {entry.capabilities.length > 0 ? entry.capabilities.join(", ") : "—"}
                  </td>
                </tr>
              )),
            )}
            {filteredEntries.length === 0 && (
              <tr>
                <td colSpan={6} className="px-3 py-8 text-center text-muted-foreground">
                  No topics found.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
