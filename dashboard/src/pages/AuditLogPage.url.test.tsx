/**
 * AuditLogPage URL-state integration tests.
 *
 * Block A from the task-55f813b3 step-3 test plan: nuqs URL roundtrip via
 * NuqsTestingAdapter + MSW. Asserts that filter state seeded from URL
 * dispatches the expected backend fetch params, and that filter mutations
 * propagate to the URL.
 *
 * Helpers tested in AuditLogPage.test.tsx (parseSeqParam etc) coexist —
 * that file uses vi.mock("@/api/client") which would conflict with MSW
 * intercepts, so the new render-based suite lives here.
 */
import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { NuqsTestingAdapter, type UrlUpdateEvent } from "nuqs/adapters/testing";
import { fireEvent, renderWithProviders, waitFor } from "@/test-utils/render";
import { server } from "@/test-utils/msw";
import AuditLogPage from "./AuditLogPage";

function emptyAuditPage() {
  return HttpResponse.json({
    items: [],
    next_cursor: "",
    returned: 0,
  });
}

describe("AuditLogPage nuqs URL roundtrip", () => {
  it("forwards ?action= URL param to the backend fetch as event_type", async () => {
    let capturedUrl: URL | null = null;
    server.use(
      http.get("*/api/v1/audit/events", ({ request }) => {
        capturedUrl = new URL(request.url);
        return emptyAuditPage();
      }),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="?action=mcp.tool_invocation">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(capturedUrl).not.toBeNull();
    });
    // Page keeps ?action= in URL for backward compat; on the wire the
    // new SIEM endpoint accepts event_type.
    expect(capturedUrl!.searchParams.get("event_type")).toBe("mcp.tool_invocation");
  });

  it("forwards ?search= and ?from= URL params to the backend fetch with date ISO conversion", async () => {
    let capturedUrl: URL | null = null;
    server.use(
      http.get("*/api/v1/audit/events", ({ request }) => {
        capturedUrl = new URL(request.url);
        return emptyAuditPage();
      }),
    );

    renderWithProviders(
      <NuqsTestingAdapter searchParams="?search=user-5&agent=agent-alpha&from=2026-04-01">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    await waitFor(() => {
      expect(capturedUrl).not.toBeNull();
    });
    expect(capturedUrl!.searchParams.get("search")).toBe("user-5");
    // agent_id is not a server-side filter on /audit/events — applied
    // client-side over the actor field. ?from= now maps to the
    // SIEM-feed `from` param (no rename for date filters).
    expect(capturedUrl!.searchParams.get("from")).toBe("2026-04-01T00:00:00.000Z");
  });

  it("clear-filters button strips all filter params from URL", async () => {
    let lastUrlUpdate: UrlUpdateEvent | null = null;

    const { container } = renderWithProviders(
      <NuqsTestingAdapter
        searchParams="?action=job.failed&search=foo&agent=agent-x&from=2026-04-01&to=2026-04-30"
        onUrlUpdate={(event) => {
          lastUrlUpdate = event;
        }}
      >
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    const clearBtn = Array.from(container.querySelectorAll("button")).find(
      (b) => b.textContent?.includes("Clear filters"),
    );
    expect(clearBtn).toBeTruthy();
    fireEvent.click(clearBtn!);

    await waitFor(() => {
      expect(lastUrlUpdate).not.toBeNull();
    });
    // After all five setX(null) calls with clearOnDefault, URL should drop them.
    expect(lastUrlUpdate!.searchParams.get("action")).toBeNull();
    expect(lastUrlUpdate!.searchParams.get("search")).toBeNull();
    expect(lastUrlUpdate!.searchParams.get("agent")).toBeNull();
    expect(lastUrlUpdate!.searchParams.get("from")).toBeNull();
    expect(lastUrlUpdate!.searchParams.get("to")).toBeNull();
  });

  it("malformed ?from= URL value defaults cleanly without throwing the query (adversarial item a)", async () => {
    let capturedUrl: URL | null = null;
    server.use(
      http.get("*/api/v1/audit/events", ({ request }) => {
        capturedUrl = new URL(request.url);
        return emptyAuditPage();
      }),
    );

    const { container } = renderWithProviders(
      <NuqsTestingAdapter searchParams="?from=banana&action=job.failed">
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    // Page renders, query fires, no Invalid Date crash. Action filter is
    // still forwarded; malformed `from` is silently dropped so the rest
    // of the filter set survives.
    await waitFor(() => {
      expect(capturedUrl).not.toBeNull();
    });
    expect(capturedUrl!.searchParams.get("event_type")).toBe("job.failed");
    expect(capturedUrl!.searchParams.get("from")).toBeNull();
    expect(container.textContent).not.toContain("Failed to load audit log");
  });

  it("selecting an event-type option pushes ?action= to URL (backward-compat URL key)", async () => {
    let lastUrlUpdate: UrlUpdateEvent | null = null;

    const { container } = renderWithProviders(
      <NuqsTestingAdapter
        searchParams=""
        onUrlUpdate={(event) => {
          lastUrlUpdate = event;
        }}
      >
        <AuditLogPage />
      </NuqsTestingAdapter>,
    );

    const actionSelect = container.querySelector(
      'select[aria-label="Filter by event type"]',
    ) as HTMLSelectElement | null;
    expect(actionSelect).toBeTruthy();
    fireEvent.change(actionSelect!, {
      target: { value: "mcp.tool_invocation" },
    });

    await waitFor(() => {
      expect(lastUrlUpdate).not.toBeNull();
    });
    expect(lastUrlUpdate!.searchParams.get("action")).toBe(
      "mcp.tool_invocation",
    );
  });
});
