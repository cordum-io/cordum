import { describe, it, expect } from "vitest";
import { runStatusMeta, jobStatusMeta, approvalStatusMeta } from "./status";

describe("runStatusMeta", () => {
  it("maps succeeded to completed success state", () => {
    const meta = runStatusMeta("succeeded");
    expect(meta.tone).toBe("success");
    expect(meta.label).toBe("completed");
  });

  it("maps running to accent tone", () => {
    expect(runStatusMeta("running").tone).toBe("accent");
  });

  it("maps failed to danger tone", () => {
    expect(runStatusMeta("failed").tone).toBe("danger");
  });

  it("maps timed_out to danger tone", () => {
    expect(runStatusMeta("timed_out").tone).toBe("danger");
  });

  it("maps pending to warning tone", () => {
    expect(runStatusMeta("pending").tone).toBe("warning");
    expect(runStatusMeta("pending").label).toBe("queued");
  });

  it("maps blocked/denied variants to governance blocked state", () => {
    expect(runStatusMeta("blocked").tone).toBe("governance");
    expect(runStatusMeta("blocked").label).toBe("blocked");
    expect(runStatusMeta("denied").label).toBe("blocked");
  });

  it("maps cancelled to failed tone", () => {
    expect(runStatusMeta("cancelled").tone).toBe("danger");
    expect(runStatusMeta("cancelled").label).toBe("failed");
  });

  it("returns muted for unknown status", () => {
    const meta = runStatusMeta("bogus");
    expect(meta.tone).toBe("muted");
    expect(meta.label).toBe("bogus");
  });

  it("returns 'unknown' label for undefined", () => {
    expect(runStatusMeta(undefined).label).toBe("unknown");
  });
});

describe("jobStatusMeta", () => {
  it("maps succeeded to success tone with diamond shape", () => {
    const meta = jobStatusMeta("succeeded");
    expect(meta.tone).toBe("success");
    expect(meta.shape).toBe("diamond");
  });

  it("maps approval_required to warning with shield shape", () => {
    const meta = jobStatusMeta("approval_required");
    expect(meta.tone).toBe("warning");
    expect(meta.shape).toBe("shield");
  });

  it("maps denied to governance tone", () => {
    expect(jobStatusMeta("denied").tone).toBe("governance");
  });

  it("returns muted for unknown state", () => {
    expect(jobStatusMeta(undefined).label).toBe("unknown");
  });
});

describe("approvalStatusMeta", () => {
  it("returns warning when approval required", () => {
    const meta = approvalStatusMeta(true);
    expect(meta.tone).toBe("warning");
    expect(meta.label).toBe("approval required");
  });

  it("returns muted when no approval needed", () => {
    const meta = approvalStatusMeta(false);
    expect(meta.tone).toBe("muted");
    expect(meta.label).toBe("no approval");
  });

  it("returns muted for undefined", () => {
    expect(approvalStatusMeta(undefined).tone).toBe("muted");
  });
});
