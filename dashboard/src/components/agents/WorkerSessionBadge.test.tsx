import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";

import type { Worker } from "@/api/types";
import { WorkerSessionBadge, formatHeartbeatAge } from "./WorkerSessionBadge";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function makeWorker(overrides: Partial<Worker> = {}): Worker {
  return {
    id: "w1",
    name: "w1",
    pool: "pool-a",
    capabilities: [],
    status: "idle",
    activeJobs: 0,
    capacity: 2,
    ...overrides,
  } as Worker;
}

function render(node: React.ReactElement): {
  container: HTMLElement;
  cleanup: () => void;
} {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(node);
  });
  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

describe("WorkerSessionBadge — DoD-required visual states", () => {
  it("renders Trusted + fresh-heartbeat sub-line when session is valid and hb is fresh", () => {
    const { container, cleanup } = render(
      <WorkerSessionBadge
        worker={makeWorker({
          online: true,
          sessionValid: true,
          sessionState: "valid",
          heartbeatAgeSeconds: 5,
          lastHeartbeatAt: new Date().toISOString(),
        })}
      />,
    );
    try {
      expect(container.textContent ?? "").toContain("Trusted");
      const age = container.querySelector("[data-testid='worker-heartbeat-age']");
      expect(age?.textContent).toBe("last hb 5s ago");
    } finally {
      cleanup();
    }
  });

  it("renders Trusted + stale-heartbeat sub-line when session is valid and hb is stale", () => {
    // This is the load-bearing demotion invariant: a stale heartbeat
    // must NOT change the status badge away from Trusted. We still
    // surface the age so operators can see freshness, but the badge
    // stays green.
    const { container, cleanup } = render(
      <WorkerSessionBadge
        worker={makeWorker({
          online: true,
          sessionValid: true,
          sessionState: "valid",
          heartbeatAgeSeconds: 420, // 7 minutes
          lastHeartbeatAt: new Date(Date.now() - 420_000).toISOString(),
        })}
      />,
    );
    try {
      expect(container.textContent ?? "").toContain("Trusted");
      const age = container.querySelector("[data-testid='worker-heartbeat-age']");
      expect(age?.textContent).toBe("last hb 7m ago");
    } finally {
      cleanup();
    }
  });

  it("renders Revoked when session is revoked even if heartbeat is fresh", () => {
    const { container, cleanup } = render(
      <WorkerSessionBadge
        worker={makeWorker({
          online: false,
          sessionValid: false,
          sessionRevoked: true,
          sessionState: "session_revoked",
          heartbeatAgeSeconds: 2,
        })}
      />,
    );
    try {
      expect(container.textContent ?? "").toContain("Revoked");
      // Fresh heartbeat line is still shown — but the badge is danger.
      const age = container.querySelector("[data-testid='worker-heartbeat-age']");
      expect(age?.textContent).toBe("last hb 2s ago");
    } finally {
      cleanup();
    }
  });

  it("renders Expired when session has expired", () => {
    const { container, cleanup } = render(
      <WorkerSessionBadge
        worker={makeWorker({
          online: false,
          sessionValid: false,
          sessionState: "session_expired",
          heartbeatAgeSeconds: 30,
        })}
      />,
    );
    try {
      expect(container.textContent ?? "").toContain("Expired");
    } finally {
      cleanup();
    }
  });

  it("renders No session when the worker never handshook", () => {
    const { container, cleanup } = render(
      <WorkerSessionBadge
        worker={makeWorker({
          online: false,
          sessionValid: false,
          sessionState: "no_session",
        })}
      />,
    );
    try {
      expect(container.textContent ?? "").toContain("No session");
    } finally {
      cleanup();
    }
  });

  it("falls back to operational status when no session signal is present", () => {
    // Legacy deploy: gateway has no trust resolver wired, so the
    // response carries neither sessionState nor online. The badge
    // falls back to the old busy/idle operational label.
    const { container, cleanup } = render(
      <WorkerSessionBadge
        worker={makeWorker({
          status: "busy",
        })}
      />,
    );
    try {
      expect(container.textContent ?? "").toContain("Busy");
    } finally {
      cleanup();
    }
  });

  it("honours `online` boolean fallback when sessionState is absent", () => {
    const { container, cleanup } = render(
      <WorkerSessionBadge
        worker={makeWorker({
          online: true,
        })}
      />,
    );
    try {
      expect(container.textContent ?? "").toContain("Online");
    } finally {
      cleanup();
    }
  });

  it("suppresses the heartbeat line when suppressHeartbeatLine is true", () => {
    const { container, cleanup } = render(
      <WorkerSessionBadge
        worker={makeWorker({
          sessionState: "valid",
          online: true,
          heartbeatAgeSeconds: 10,
        })}
        suppressHeartbeatLine
      />,
    );
    try {
      expect(
        container.querySelector("[data-testid='worker-heartbeat-age']"),
      ).toBeNull();
    } finally {
      cleanup();
    }
  });
});

describe("formatHeartbeatAge — unit boundaries", () => {
  it.each([
    [0, "last hb 0s ago"],
    [1, "last hb 1s ago"],
    [59, "last hb 59s ago"],
    [60, "last hb 1m ago"],
    [119, "last hb 2m ago"],
    [3600, "last hb 1h ago"],
    [7200, "last hb 2h ago"],
    [86400, "last hb 1d ago"],
  ])("formats age=%d seconds as %s", (age, expected) => {
    expect(formatHeartbeatAge(age)).toBe(expected);
  });

  it("returns null when age is not a finite number", () => {
    expect(formatHeartbeatAge(undefined)).toBeNull();
    expect(formatHeartbeatAge(Number.NaN)).toBeNull();
    expect(formatHeartbeatAge(Number.POSITIVE_INFINITY)).toBeNull();
  });

  it("clamps negative ages to 0s (clock-skew safety)", () => {
    expect(formatHeartbeatAge(-30)).toBe("last hb 0s ago");
  });
});
