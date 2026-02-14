import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { generateAuditReport } from "./audit-report";
import type { AuditEntry } from "../api/types";
import type { AuditFilters } from "../hooks/useAudit";

interface MockDoc {
  setFontSize: ReturnType<typeof vi.fn>;
  setFont: ReturnType<typeof vi.fn>;
  setTextColor: ReturnType<typeof vi.fn>;
  text: ReturnType<typeof vi.fn>;
  setDrawColor: ReturnType<typeof vi.fn>;
  setLineWidth: ReturnType<typeof vi.fn>;
  line: ReturnType<typeof vi.fn>;
  addPage: ReturnType<typeof vi.fn>;
  getNumberOfPages: ReturnType<typeof vi.fn>;
  setPage: ReturnType<typeof vi.fn>;
  save: ReturnType<typeof vi.fn>;
  splitTextToSize: ReturnType<typeof vi.fn>;
}

const { JsPdfMock, createdDocs } = vi.hoisted(() => {
  const created: MockDoc[] = [];
  class MockJsPdfClass implements MockDoc {
    private pages = 1;
    setFontSize = vi.fn();
    setFont = vi.fn();
    setTextColor = vi.fn();
    text = vi.fn();
    setDrawColor = vi.fn();
    setLineWidth = vi.fn();
    line = vi.fn();
    addPage = vi.fn(() => {
      this.pages += 1;
    });
    getNumberOfPages = vi.fn(() => this.pages);
    setPage = vi.fn();
    save = vi.fn();
    splitTextToSize = vi.fn((text: string) => [String(text)]);

    constructor() {
      created.push(this);
    }
  }
  return { JsPdfMock: MockJsPdfClass, createdDocs: created };
});

vi.mock("jspdf", () => ({
  jsPDF: JsPdfMock,
}));

function sampleEvent(id: string, overrides: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id,
    timestamp: "2026-02-13T10:00:00.000Z",
    eventType: "policy.update",
    actor: "alice",
    resourceType: "policy_bundle",
    resourceId: "bundle-1",
    action: "approve",
    message: "policy approved",
    category: "human_action",
    severity: "medium",
    ...overrides,
  };
}

describe("generateAuditReport", () => {
  beforeEach(() => {
    createdDocs.length = 0;
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-13T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("generates and saves a report PDF with progress callback", async () => {
    const events: AuditEntry[] = [
      sampleEvent("e1"),
      sampleEvent("e2", {
        timestamp: "2026-02-14T11:00:00.000Z",
        category: "safety_decision",
        severity: "high",
        action: "deny",
      }),
    ];
    const filters: AuditFilters = {
      eventType: ["policy.update"],
      actor: "alice",
      timeRange: "7d",
    };
    const onProgress = vi.fn();

    await generateAuditReport(events, filters, "tester", onProgress);

    const doc = createdDocs[0];
    expect(doc.text).toHaveBeenCalledWith("Cordum Audit Report", 15, 45);
    expect(doc.addPage).toHaveBeenCalled();
    expect(doc.save).toHaveBeenCalledWith("cordum-audit-report-2026-02-13.pdf");
    expect(onProgress).toHaveBeenLastCalledWith(2);
  });

  it("handles empty event sets and still saves report", async () => {
    await generateAuditReport([], {}, "tester");

    const doc = createdDocs[0];
    expect(doc.save).toHaveBeenCalledWith("cordum-audit-report-2026-02-13.pdf");
    expect(doc.getNumberOfPages).toHaveBeenCalled();
  });
});
