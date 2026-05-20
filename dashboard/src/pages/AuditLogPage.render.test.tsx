/**
 * AuditLogPage render-level integration tests.
 *
 * Block B (DataTable rendering), Block C (page states), Block D
 * (drilldown for amended DoD #4 via comment-832277b0). Coexists with:
 *  - AuditLogPage.test.tsx (pure-helper tests via vi.mock)
 *  - AuditLogPage.url.test.tsx (Block A nuqs URL roundtrip)
 *
 * Block D covers the DoD #4 amendment that moved the per-row signature
 * pill into a row-click Drawer drilldown deriving per-event verdict
 * from the cached chain-wide /audit/verify result.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import {
  fireEvent,
  renderWithProviders,
  waitFor,
} from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import { useConfigStore } from "@/state/config";
import AuditLogPage from "./AuditLogPage";

// Shape consumed by the SIEM-feed endpoint /api/v1/audit/events. Mirrors
// the OpenAPI `AuditEvent` schema (orval-generated at
// src/api/generated/model/auditEvent.ts) closely enough that the page's
// mapEvent helper produces the same on-screen rows as the legacy
// policy-bundle path; only the wire shape changed.
interface RawAuditEntry {
  id: string;
  seq?: number;
  timestamp: string;
  event_type: string;
  severity: string;
  tenant_id: string;
  action: string;
  identity?: string;
  agent_id?: string;
  job_id?: string;
  decision?: string;
  reason?: string;
  extra?: Record<string, string>;
}

// makeEntry deliberately leaves `seq` undefined so the per-test
// override controls the chain-membership story (the drilldown
// pending/verified/unverified branches gate on whether `seq` is present
// and where it falls relative to the cached chain). Tests that exercise
// chain assertions pass `seq:` explicitly.
function makeEntry(
  index: number,
  overrides: Partial<RawAuditEntry> = {},
): RawAuditEntry {
  return {
    id: `evt-${String(index).padStart(4, "0")}`,
    timestamp: new Date(Date.now() - index * 60_000).toISOString(),
    event_type: "job.created",
    severity: "INFO",
    tenant_id: "default",
    action: "job.created",
    identity: `user-${index}`,
    extra: { resource_id: `job-${index}` },
    reason: `Test event ${index}`,
    ...overrides,
  };
}

let latestIntersectionCallback: IntersectionObserverCallback | null = null;

class MockIntersectionObserver {
  readonly root = null;
  readonly rootMargin: string;
  readonly thresholds: ReadonlyArray<number> = [];

  constructor(
    callback: IntersectionObserverCallback,
    options?: IntersectionObserverInit,
  ) {
    latestIntersectionCallback = callback;
    this.rootMargin = options?.rootMargin ?? "";
  }

  disconnect = vi.fn();
  observe = vi.fn();
  takeRecords = vi.fn((): IntersectionObserverEntry[] => []);
  unobserve = vi.fn();
}

beforeEach(() => {
  latestIntersectionCallback = null;
  vi.stubGlobal("IntersectionObserver", MockIntersectionObserver);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function triggerAuditSentinel(isIntersecting: boolean): void {
  latestIntersectionCallback?.(
    [{ isIntersecting } as IntersectionObserverEntry],
    {} as IntersectionObserver,
  );
}

describe("AuditLogPage Block B — DataTable rendering", () => {
  it("decision-identity 3px left edge: rows carry data-decision-tier per decision", async () => {
    server.use(
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [
            makeEntry(1, { decision: "deny", action: "approval.decided" }),
            makeEntry(2, { decision: "allow", action: "approval.decided" }),
            makeEntry(3, { decision: "require_approval" }),
            makeEntry(4),
          ],
          total: 4,
          next_cursor: "",
          returned: 0,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("[data-decision-tier='deny']")).toBeTruthy();
    });
    expect(
      container.querySelector("[data-decision-tier='allow']"),
    ).toBeTruthy();
    expect(
      container.querySelector("[data-decision-tier='require_approval']"),
    ).toBeTruthy();
    // Event 4 has no decision — its row should not carry data-decision-tier.
    const rowsWithoutTier = Array.from(
      container.querySelectorAll("tbody tr"),
    ).filter((r) => !r.getAttribute("data-decision-tier"));
    expect(rowsWithoutTier.length).toBeGreaterThan(0);
  });

  it("uses page scrolling for row count > 100 instead of the fixed virtualized box", async () => {
    const items: RawAuditEntry[] = Array.from({ length: 125 }, (_, i) =>
      makeEntry(i),
    );
    server.use(
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items,
          total: 125,
          next_cursor: "",
          returned: 0,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelectorAll("tbody tr").length).toBe(125);
    });
    expect(container.querySelector("[data-virtualized='true']")).toBeNull();
    expect(container.textContent).toContain("End of audit trail.");
  });

  it("non-virtualized path renders all rows when count <= 100", async () => {
    const items: RawAuditEntry[] = Array.from({ length: 25 }, (_, i) =>
      makeEntry(i),
    );
    server.use(
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items,
          total: 25,
          next_cursor: "",
          returned: 0,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelectorAll("tbody tr").length).toBe(25);
    });
    expect(
      container.querySelector("[data-virtualized='true']"),
    ).toBeNull();
  });

  it("loads the next cursor page when the audit trail sentinel intersects", async () => {
    const requestedCursors: string[] = [];
    server.use(
      http.get("*/api/v1/audit/events", ({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor") ?? "";
        requestedCursors.push(cursor);

        if (cursor === "") {
          return HttpResponse.json({
            items: [makeEntry(1, { id: "evt-page-1" })],
            total: 2,
            next_cursor: "cursor-2",
            returned: 1,
          });
        }

        return HttpResponse.json({
          items: [makeEntry(2, { id: "evt-page-2" })],
          total: 2,
          next_cursor: "",
          returned: 1,
        });
      }),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelectorAll("tbody tr").length).toBe(1);
      expect(container.textContent).toContain("Scroll for older audit events.");
    });
    expect(container.textContent).toContain("Load more");

    triggerAuditSentinel(true);
    triggerAuditSentinel(true);

    await waitFor(() => {
      expect(requestedCursors).toContain("cursor-2");
      expect(container.querySelectorAll("tbody tr").length).toBe(2);
    });
    expect(requestedCursors.filter((cursor) => cursor === "cursor-2")).toHaveLength(1);
    expect(container.textContent).toContain("End of audit trail.");
  });
});

describe("AuditLogPage Block C — page states", () => {
  it("renders EmptyState when API returns []", async () => {
    server.use(
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [],
          total: 0,
          next_cursor: "",
          returned: 0,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.textContent).toContain("No audit events");
    });
  });

  it("renders ErrorBanner with retry on 500", async () => {
    server.use(
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json(
          { error: "internal" },
          { status: 500 },
        ),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    // ErrorBanner renders its default "Something went wrong" header
    // plus the API error body (`error: "internal"`) and a Retry button.
    // The page's banner contract is: a 5xx must surface a retryable
    // error state — assert on the Retry control rather than guessing
    // which word the upstream error message happens to use.
    await waitFor(() => {
      const retryBtn = Array.from(container.querySelectorAll("button")).find(
        (b) => b.textContent?.toLowerCase().includes("retry"),
      );
      expect(retryBtn).toBeTruthy();
    });
    expect(container.textContent?.toLowerCase()).toMatch(
      /something went wrong|failed|error|internal/,
    );
  });
});

// Block D — Drilldown drawer covers the amended DoD #4 contract:
// row click opens the drawer; chain-signature section derives per-event
// verdict from the cached /audit/verify result.

function setUserRole(role: string | null): void {
  // Direct setState on the Zustand slice — config store exposes only the
  // tenant-switch helper publicly, so test setup reaches into setState
  // directly. Cleanup runs in afterEach via the Vitest test isolation.
  useConfigStore.setState({
    principalRole: role ?? undefined,
    user: role
      ? {
          id: "test-admin",
          username: "test-admin",
          email: "admin@test",
          display_name: "Test Admin",
          roles: [role],
          tenant: "tenant-test",
        }
      : null,
    tenantId: "tenant-test",
  });
}

function defaultAuthConfigEnforcing() {
  // Auth config that makes usePermission run a real role check (so
  // useIsAdmin gates on user.roles instead of the no-auth bypass).
  return http.get("*/api/v1/auth/config", () =>
    HttpResponse.json({
      password_enabled: true,
      user_auth_enabled: false,
      saml_enabled: false,
      oidc_enabled: false,
    }),
  );
}

describe("AuditLogPage Block D — drilldown drawer (DoD #4 amended)", () => {
  it("clicking a row opens the drilldown drawer with event metadata", async () => {
    setUserRole("admin");
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [
            makeEntry(1, {
              id: "evt-detail-target",
              action: "policy.updated",
              identity: "alice",
              decision: "allow",
            }),
          ],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
      http.get("*/api/v1/audit/verify", () =>
        HttpResponse.json({
          status: "ok",
          total_events: 0,
          verified_events: 0,
          gaps: [],
          retention_boundary_seq: 0,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    const firstRow = container.querySelector("tbody tr") as HTMLElement;
    fireEvent.click(firstRow);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    expect(container.textContent).toContain("evt-detail-target");
  });

  it("drilldown shows 'Pending' for an event without seq (absent from cached chain result)", async () => {
    setUserRole("admin");
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [makeEntry(1, { id: "evt-no-seq" })],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
      http.get("*/api/v1/audit/verify", () =>
        HttpResponse.json({
          status: "ok",
          total_events: 100,
          verified_events: 100,
          gaps: [],
          retention_boundary_seq: 0,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    fireEvent.click(container.querySelector("tbody tr") as HTMLElement);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    await waitFor(() => {
      expect(container.textContent).toContain("Pending");
    });
    expect(container.textContent).toContain("policy-only audit entries");
  });

  it("drilldown shows 'Verified' for an event whose seq is within window and not in gaps", async () => {
    setUserRole("admin");
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [makeEntry(1, { id: "evt-verified", seq: 250 })],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
      http.get("*/api/v1/audit/verify", () =>
        HttpResponse.json({
          status: "ok",
          total_events: 500,
          verified_events: 500,
          gaps: [],
          retention_boundary_seq: 100,
          first_seq: 100,
          last_seq: 500,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    fireEvent.click(container.querySelector("tbody tr") as HTMLElement);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    await waitFor(() => {
      expect(container.textContent).toContain("Verified");
    });
  });

  it("drilldown shows 'Unverified' when event seq is in gaps as hash_mismatch", async () => {
    setUserRole("admin");
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [makeEntry(1, { id: "evt-tampered", seq: 42 })],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
      http.get("*/api/v1/audit/verify", () =>
        HttpResponse.json({
          status: "compromised",
          total_events: 100,
          verified_events: 99,
          gaps: [{ at_seq: 42, type: "hash_mismatch" }],
          retention_boundary_seq: 0,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    fireEvent.click(container.querySelector("tbody tr") as HTMLElement);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    await waitFor(() => {
      expect(container.textContent).toContain("Unverified");
    });
    expect(container.textContent).toContain("tamper detected");
  });

  it("drilldown shows 'Pending' for event seq below retention boundary (pruned)", async () => {
    setUserRole("admin");
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [makeEntry(1, { id: "evt-trimmed", seq: 5 })],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
      http.get("*/api/v1/audit/verify", () =>
        HttpResponse.json({
          status: "partial",
          total_events: 100,
          verified_events: 100,
          gaps: [],
          retention_boundary_seq: 50,
          retention_window_hours: 168,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    fireEvent.click(container.querySelector("tbody tr") as HTMLElement);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    await waitFor(() => {
      expect(container.textContent).toContain("Pending");
    });
    expect(container.textContent).toContain("retention-trimmed");
  });

  it("drilldown shows 'requires admin role' hint for non-admin viewer", async () => {
    setUserRole(null);
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [makeEntry(1, { id: "evt-non-admin", seq: 200 })],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    fireEvent.click(container.querySelector("tbody tr") as HTMLElement);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    expect(container.textContent).toMatch(
      /requires admin role|admin role|govern\/verification/i,
    );
  });

  // Pending badge state per amended DoD #4 (comment-7419de07, third
  // amendment): chain verdict not yet loaded → muted "Pending" badge so
  // the row keeps rendering and the operator sees a clear "we don't know
  // yet" rather than a misleading neutral state. The verify request
  // never resolves in this test so the drilldown parks on Pending.
  it("drilldown shows muted 'Pending' badge while /audit/verify is in-flight (state #1: chain verdict not yet loaded)", async () => {
    setUserRole("admin");
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [makeEntry(1, { id: "evt-pending", seq: 300 })],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
      http.get(
        "*/api/v1/audit/verify",
        () =>
          // Never resolves — keeps the query in the loading state so the
          // drilldown parks on the Pending badge.
          new Promise<Response>(() => {}),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    fireEvent.click(container.querySelector("tbody tr") as HTMLElement);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    await waitFor(() => {
      expect(container.textContent).toContain("Pending");
    });
    expect(container.textContent).toContain("Loading chain verification");
  });

  // Pending state #2 per amended DoD #4: chain verdict request errored
  // → still pending (we cannot claim verified or unverified without a
  // result). The signature display must NOT block row rendering.
  it("drilldown shows muted 'Pending' badge when /audit/verify request errors (state #2: result unavailable)", async () => {
    setUserRole("admin");
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [makeEntry(1, { id: "evt-error", seq: 400 })],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
      http.get("*/api/v1/audit/verify", () =>
        HttpResponse.json({ error: "verify unavailable" }, { status: 500 }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    fireEvent.click(container.querySelector("tbody tr") as HTMLElement);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    await waitFor(() => {
      expect(container.textContent).toContain("Pending");
    });
    expect(container.textContent).toContain("evt-error");
    expect(container.textContent).toContain("Chain verification result unavailable");
  });

  // Pending state #5 per amended DoD #4 — QA reopen #3 finding (msg-1947335a):
  // a row's seq above the cached chain's last_seq is "absent from the cached
  // result" by the architect's pending definition (comment-7419de07). Without
  // this guard, a bounded/limited verify window would default-pass an out-of-
  // range seq as Verified.
  it("drilldown shows 'Pending' when event seq is above chain.last_seq (outside cached range)", async () => {
    setUserRole("admin");
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [makeEntry(1, { id: "evt-out-of-range", seq: 999 })],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
      http.get("*/api/v1/audit/verify", () =>
        HttpResponse.json({
          status: "ok",
          total_events: 500,
          verified_events: 500,
          gaps: [],
          retention_boundary_seq: 100,
          first_seq: 100,
          last_seq: 500,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    fireEvent.click(container.querySelector("tbody tr") as HTMLElement);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    await waitFor(() => {
      expect(container.textContent).toContain("Pending");
    });
    expect(container.textContent).toContain("outside the cached verification range");
    expect(container.textContent).toContain("100");
    expect(container.textContent).toContain("500");
  });

  // Pending state #6 per amended DoD #4 — QA reopen #3 finding: an empty
  // verify result (verified_events=0) means the chain has not verified any
  // event yet; every row's seq is "absent from the cached result" per the
  // architect's pending definition.
  it("drilldown shows 'Pending' when chain returns empty verified result (verified_events=0)", async () => {
    setUserRole("admin");
    server.use(
      defaultAuthConfigEnforcing(),
      http.get("*/api/v1/audit/events", () =>
        HttpResponse.json({
          items: [makeEntry(1, { id: "evt-empty-chain", seq: 250 })],
          total: 1,
          next_cursor: "",
          returned: 0,
        }),
      ),
      http.get("*/api/v1/audit/verify", () =>
        HttpResponse.json({
          status: "ok",
          total_events: 0,
          verified_events: 0,
          gaps: [],
          retention_boundary_seq: 0,
        }),
      ),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(container.querySelector("tbody tr")).toBeTruthy();
    });
    fireEvent.click(container.querySelector("tbody tr") as HTMLElement);

    await waitFor(() => {
      expect(container.querySelector("[role='dialog']")).toBeTruthy();
    });
    await waitFor(() => {
      expect(container.textContent).toContain("Pending");
    });
    expect(container.textContent).toContain("no events verified yet");
  });
});
