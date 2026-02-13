import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { captureElement, exportPdf } from "./pdfExport";

interface MockDoc {
  setFontSize: ReturnType<typeof vi.fn>;
  setFont: ReturnType<typeof vi.fn>;
  setTextColor: ReturnType<typeof vi.fn>;
  text: ReturnType<typeof vi.fn>;
  setDrawColor: ReturnType<typeof vi.fn>;
  setLineWidth: ReturnType<typeof vi.fn>;
  line: ReturnType<typeof vi.fn>;
  splitTextToSize: ReturnType<typeof vi.fn>;
  setFillColor: ReturnType<typeof vi.fn>;
  rect: ReturnType<typeof vi.fn>;
  addImage: ReturnType<typeof vi.fn>;
  addPage: ReturnType<typeof vi.fn>;
  getNumberOfPages: ReturnType<typeof vi.fn>;
  setPage: ReturnType<typeof vi.fn>;
  save: ReturnType<typeof vi.fn>;
}

const { JsPdfMock, html2canvasMock, createdDocs } = vi.hoisted(() => {
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
    splitTextToSize = vi.fn((text: string) => [String(text)]);
    setFillColor = vi.fn();
    rect = vi.fn();
    addImage = vi.fn();
    addPage = vi.fn(() => {
      this.pages += 1;
    });
    getNumberOfPages = vi.fn(() => this.pages);
    setPage = vi.fn();
    save = vi.fn();

    constructor() {
      created.push(this);
    }
  }
  const canvasMock = vi.fn();
  return { JsPdfMock: MockJsPdfClass, html2canvasMock: canvasMock, createdDocs: created };
});

vi.mock("jspdf", () => ({
  jsPDF: JsPdfMock,
}));

vi.mock("html2canvas", () => ({
  default: html2canvasMock,
}));

describe("pdfExport", () => {
  beforeEach(() => {
    createdDocs.length = 0;
    html2canvasMock.mockReset();
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-02-13T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("captureElement returns PNG data url from html2canvas", async () => {
    const element = document.createElement("div");
    html2canvasMock.mockResolvedValue({
      width: 100,
      height: 50,
      toDataURL: vi.fn(() => "data:image/png;base64,abc"),
    });

    const dataUrl = await captureElement(element);

    expect(dataUrl).toBe("data:image/png;base64,abc");
    expect(html2canvasMock).toHaveBeenCalledWith(
      element,
      expect.objectContaining({
        scale: 2,
        useCORS: true,
        backgroundColor: "#ffffff",
      }),
    );
  });

  it("exportPdf renders mixed sections and saves with slugged filename", async () => {
    const element = document.createElement("div");
    html2canvasMock.mockResolvedValue({
      width: 200,
      height: 100,
      toDataURL: vi.fn(() => "data:image/png;base64,img"),
    });

    await exportPdf({
      title: "Security Report",
      tenantName: "Tenant A",
      sections: [
        { type: "heading", content: "Overview", label: "Section" },
        { type: "text", content: "Line 1\nLine 2" },
        { type: "table", content: [["H1", "H2"], ["v1", "v2"]] },
        { type: "image", content: element, label: "Chart" },
      ],
    });

    const doc = createdDocs[0];
    expect(html2canvasMock).toHaveBeenCalledTimes(2); // captureElement + size calc
    expect(doc.addImage).toHaveBeenCalled();
    expect(doc.save).toHaveBeenCalledWith("cordum-security-report-2026-02-13.pdf");
  });

  it("writes placeholder text when image capture fails", async () => {
    const element = document.createElement("div");
    html2canvasMock.mockRejectedValue(new Error("capture failed"));

    await exportPdf({
      title: "Failure Report",
      sections: [{ type: "image", content: element, label: "Broken Chart" }],
    });

    const doc = createdDocs[0];
    expect(doc.text).toHaveBeenCalledWith(
      "[Chart could not be captured: Broken Chart]",
      15,
      expect.any(Number),
    );
    expect(doc.save).toHaveBeenCalledWith("cordum-failure-report-2026-02-13.pdf");
  });
});
