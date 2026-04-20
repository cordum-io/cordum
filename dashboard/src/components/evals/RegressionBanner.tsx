import { ShieldAlert } from "lucide-react";
import { Link } from "react-router-dom";
import { InfoBanner } from "@/components/ui/InfoBanner";
import { isRegressionRun } from "@/api/transform";
import type { EvalRun } from "@/api/types";

export function RegressionBanner({ run }: { run: EvalRun }) {
  if (!isRegressionRun(run)) return null;
  const count = run.summary.regressions;
  return (
    <div role="alert" aria-live="polite">
      <InfoBanner
        variant="error"
        title="Regression detected"
        icon={<ShieldAlert className="h-4 w-4" />}
      >
        <p>
          Latest run found {count} regression{count === 1 ? "" : "s"}. Actions
          previously denied by policy are now allowed.{" "}
          <Link
            to={`/evals/runs/${encodeURIComponent(run.runId)}`}
            className="font-semibold underline"
          >
            View run
          </Link>
        </p>
      </InfoBanner>
    </div>
  );
}
