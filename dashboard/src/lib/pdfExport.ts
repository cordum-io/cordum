import { jsPDF } from "jspdf";
import html2canvas from "html2canvas";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface PdfSection {
  type: "heading" | "table" | "image" | "text";
  content: string | string[][] | HTMLElement;
  label?: string;
}

export interface PdfOptions {
  title: string;
  tenantName?: string;
  sections: PdfSection[];
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const PAGE_WIDTH = 210; // A4 mm
const PAGE_HEIGHT = 297;
const MARGIN = 15;
const CONTENT_WIDTH = PAGE_WIDTH - MARGIN * 2;
const FOOTER_HEIGHT = 10;
const USABLE_HEIGHT = PAGE_HEIGHT - MARGIN - FOOTER_HEIGHT;

// ---------------------------------------------------------------------------
// Capture HTML element to data URL
// ---------------------------------------------------------------------------

export async function captureElement(element: HTMLElement): Promise<string> {
  const canvas = await html2canvas(element, {
    scale: 2,
    useCORS: true,
    backgroundColor: "#ffffff",
  });
  return canvas.toDataURL("image/png");
}

// ---------------------------------------------------------------------------
// PDF export
// ---------------------------------------------------------------------------

export async function exportPdf(options: PdfOptions): Promise<void> {
  const { title, tenantName, sections } = options;
  const doc = new jsPDF("p", "mm", "a4");
  let y = MARGIN;
  let pageCount = 1;

  // Track pages for footer numbering
  const addPage = () => {
    doc.addPage();
    pageCount++;
    y = MARGIN;
  };

  const ensureSpace = (needed: number) => {
    if (y + needed > USABLE_HEIGHT) {
      addPage();
    }
  };

  // ---- Header ----
  doc.setFontSize(18);
  doc.setFont("helvetica", "bold");
  doc.text("CORDUM", MARGIN, y);
  y += 7;

  doc.setFontSize(10);
  doc.setFont("helvetica", "normal");
  doc.setTextColor(100);
  if (tenantName) {
    doc.text(tenantName, MARGIN, y);
    y += 5;
  }
  doc.text(`Exported: ${new Date().toLocaleString()}`, MARGIN, y);
  y += 8;

  // Title
  doc.setFontSize(14);
  doc.setFont("helvetica", "bold");
  doc.setTextColor(0);
  doc.text(title, MARGIN, y);
  y += 4;

  // Divider line
  doc.setDrawColor(200);
  doc.setLineWidth(0.3);
  doc.line(MARGIN, y, PAGE_WIDTH - MARGIN, y);
  y += 8;

  // ---- Sections ----
  for (const section of sections) {
    switch (section.type) {
      case "heading": {
        ensureSpace(12);
        if (section.label) {
          doc.setFontSize(8);
          doc.setFont("helvetica", "normal");
          doc.setTextColor(130);
          doc.text(section.label, MARGIN, y);
          y += 4;
        }
        doc.setFontSize(12);
        doc.setFont("helvetica", "bold");
        doc.setTextColor(0);
        doc.text(String(section.content), MARGIN, y);
        y += 8;
        break;
      }

      case "text": {
        doc.setFontSize(9);
        doc.setFont("helvetica", "normal");
        doc.setTextColor(60);
        const lines = doc.splitTextToSize(String(section.content), CONTENT_WIDTH) as string[];
        ensureSpace(lines.length * 4 + 4);
        if (section.label) {
          doc.setFontSize(8);
          doc.setTextColor(130);
          doc.text(section.label, MARGIN, y);
          y += 4;
          doc.setFontSize(9);
          doc.setTextColor(60);
        }
        doc.text(lines, MARGIN, y);
        y += lines.length * 4 + 4;
        break;
      }

      case "table": {
        const rows = section.content as string[][];
        if (rows.length === 0) break;

        if (section.label) {
          ensureSpace(8);
          doc.setFontSize(8);
          doc.setFont("helvetica", "normal");
          doc.setTextColor(130);
          doc.text(section.label, MARGIN, y);
          y += 4;
        }

        const colCount = rows[0].length;
        const colWidth = CONTENT_WIDTH / colCount;
        const rowHeight = 6;

        for (let r = 0; r < rows.length; r++) {
          ensureSpace(rowHeight + 2);
          const isHeader = r === 0;

          // Row background
          if (isHeader) {
            doc.setFillColor(240, 240, 240);
            doc.rect(MARGIN, y - 4, CONTENT_WIDTH, rowHeight, "F");
          }

          doc.setFontSize(8);
          doc.setFont("helvetica", isHeader ? "bold" : "normal");
          doc.setTextColor(isHeader ? 0 : 60);

          for (let c = 0; c < colCount; c++) {
            const cellText = rows[r][c] ?? "";
            const truncated = cellText.length > 40 ? cellText.slice(0, 37) + "..." : cellText;
            doc.text(truncated, MARGIN + c * colWidth + 1, y);
          }
          y += rowHeight;

          // Row border
          doc.setDrawColor(220);
          doc.setLineWidth(0.1);
          doc.line(MARGIN, y - 2, PAGE_WIDTH - MARGIN, y - 2);
        }
        y += 4;
        break;
      }

      case "image": {
        const element = section.content as HTMLElement;
        try {
          const dataUrl = await captureElement(element);
          const canvas = await html2canvas(element, { scale: 2, backgroundColor: "#ffffff" });
          const imgWidth = CONTENT_WIDTH;
          const imgHeight = (canvas.height / canvas.width) * imgWidth;

          if (section.label) {
            ensureSpace(6);
            doc.setFontSize(8);
            doc.setFont("helvetica", "normal");
            doc.setTextColor(130);
            doc.text(section.label, MARGIN, y);
            y += 4;
          }

          ensureSpace(imgHeight + 6);
          doc.addImage(dataUrl, "PNG", MARGIN, y, imgWidth, imgHeight);
          y += imgHeight + 6;
        } catch {
          // If capture fails, add placeholder text
          ensureSpace(10);
          doc.setFontSize(9);
          doc.setFont("helvetica", "italic");
          doc.setTextColor(150);
          doc.text(`[Chart could not be captured: ${section.label ?? "unknown"}]`, MARGIN, y);
          y += 8;
        }
        break;
      }
    }
  }

  // ---- Page numbers ----
  const totalPages = doc.getNumberOfPages();
  for (let i = 1; i <= totalPages; i++) {
    doc.setPage(i);
    doc.setFontSize(8);
    doc.setFont("helvetica", "normal");
    doc.setTextColor(150);
    doc.text(`Page ${i} of ${totalPages}`, PAGE_WIDTH / 2, PAGE_HEIGHT - 8, { align: "center" });
  }

  // ---- Save ----
  const date = new Date().toISOString().split("T")[0];
  const slug = title.toLowerCase().replace(/\s+/g, "-");
  doc.save(`cordum-${slug}-${date}.pdf`);
}
