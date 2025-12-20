import type { JobState } from "../lib/api";
import { jobStateLabel } from "../lib/format";

function colorFor(state: JobState | string) {
  switch (state) {
    case "PENDING":
      return "bg-zinc-500/20 text-zinc-200 border-zinc-400/20";
    case "SCHEDULED":
    case "DISPATCHED":
      return "bg-sky-500/15 text-sky-200 border-sky-400/20";
    case "RUNNING":
      return "bg-indigo-500/15 text-indigo-200 border-indigo-400/20";
    case "SUCCEEDED":
      return "bg-emerald-500/15 text-emerald-200 border-emerald-400/20";
    case "FAILED":
      return "bg-red-500/15 text-red-200 border-red-400/20";
    case "CANCELLED":
      return "bg-amber-500/15 text-amber-200 border-amber-400/20";
    case "TIMEOUT":
      return "bg-orange-500/15 text-orange-200 border-orange-400/20";
    case "DENIED":
      return "bg-pink-500/15 text-pink-200 border-pink-400/20";
    default:
      return "bg-secondary-background/60 text-primary-text border-primary-border";
  }
}

export default function Badge({ state }: { state: JobState | string }) {
  const label = jobStateLabel(state);
  return (
    <span
      className={[
        "inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium",
        colorFor(label),
      ].join(" ")}
    >
      {label}
    </span>
  );
}
