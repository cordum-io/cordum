import { InfoBanner } from "@/components/ui/InfoBanner";

/**
 * WorkerSessionLegend explains the session-authority demotion for
 * operators scanning the agent registry. Placed above the table so
 * the change from "last heartbeat gates online" to "session token
 * gates online" is explicit.
 *
 * Reuses the existing InfoBanner primitive — no bespoke styling, so
 * the page keeps a single visual cadence for banners.
 */
export function WorkerSessionLegend() {
  return (
    <InfoBanner variant="info" title="Status source: session-token authority">
      <p>
        The <b>Status</b> column now reflects the worker's{" "}
        <span className="font-mono">session token</span> state — the
        authoritative dispatch-eligibility signal. A fresh heartbeat
        without a valid session is <b>not</b> considered online, and a
        stale heartbeat with a valid session remains dispatchable.
      </p>
      <p className="mt-1.5 text-muted-foreground">
        The <span className="font-mono">last hb Xs ago</span> sub-line
        is cosmetic freshness telemetry only — never used to gate
        policy or routing decisions.
      </p>
    </InfoBanner>
  );
}
