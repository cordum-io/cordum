// Axe-core test helper for the dashboard verification rail.
//
// Note: jsdom does not composite backdrop-filter, so axe's color-contrast
// rule fires false-negatives on glass-panel surfaces. The structural
// contrast for those surfaces was verified manually in task-84989aa0
// (muted-foreground #5a6a70 on --card #fffaf2 = 5.41:1, passes WCAG AA).
//
// The plan's import-from-@axe-core/react was wrong: that package's only
// export is a dev-mode runtime injector. For tests we drive axe-core
// directly — it's installed transitively via @axe-core/react.
import axe from "axe-core";

interface AxeOptions {
  mode?: "light" | "dark";
}

export async function assertNoSeriousAxeViolations(
  container: HTMLElement,
  options: AxeOptions = {},
): Promise<void> {
  const root = document.documentElement;
  root.classList.remove("light", "dark");
  root.classList.add(options.mode === "dark" ? "dark" : "light");

  const results = await axe.run(container, {
    runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] },
  });

  const serious = results.violations.filter(
    (v) => v.impact === "critical" || v.impact === "serious",
  );

  if (serious.length === 0) return;

  const summary = serious
    .map((v) => {
      const nodeDetail = v.nodes
        .slice(0, 3)
        .map((n) => `      target=${n.target.join(",")} | failure=${n.failureSummary?.split("\n").join(" / ")}`)
        .join("\n");
      return `  ${v.id} (${v.impact}): ${v.description} — ${v.nodes.length} node(s)\n${nodeDetail}`;
    })
    .join("\n");
  throw new Error(`Axe critical+serious violations:\n${summary}`);
}
