import { describe, it, expect } from "vitest";
import { vi } from "vitest";
import { downloadFile, toCsv } from "./export";

describe("toCsv", () => {
  it("produces header + data rows", () => {
    const csv = toCsv(["Name", "Age"], [["Alice", "30"], ["Bob", "25"]]);
    expect(csv).toBe("Name,Age\nAlice,30\nBob,25");
  });

  it("escapes commas in values", () => {
    const csv = toCsv(["Value"], [["hello, world"]]);
    expect(csv).toBe('Value\n"hello, world"');
  });

  it("escapes double quotes", () => {
    const csv = toCsv(["Value"], [['say "hi"']]);
    expect(csv).toBe('Value\n"say ""hi"""');
  });

  it("escapes newlines in values", () => {
    const csv = toCsv(["Value"], [["line1\nline2"]]);
    expect(csv).toBe('Value\n"line1\nline2"');
  });

  it("handles empty rows", () => {
    const csv = toCsv(["A", "B"], []);
    expect(csv).toBe("A,B");
  });

  it("handles empty headers and rows", () => {
    const csv = toCsv([], []);
    expect(csv).toBe("");
  });
});

describe("downloadFile", () => {
  it("creates a blob URL, clicks anchor, and cleans up DOM/url", () => {
    const createObjectURLSpy = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:mock-url");
    const revokeObjectURLSpy = vi
      .spyOn(URL, "revokeObjectURL")
      .mockImplementation(() => {});

    const realCreateElement = document.createElement.bind(document);
    const anchor = realCreateElement("a");
    const clickSpy = vi.spyOn(anchor, "click").mockImplementation(() => {});
    const createElementSpy = vi
      .spyOn(document, "createElement")
      .mockImplementation((tagName: string): HTMLElement => {
        if (tagName.toLowerCase() === "a") {
          return anchor;
        }
        return realCreateElement(tagName);
      });

    const appendSpy = vi.spyOn(document.body, "appendChild");
    const removeSpy = vi.spyOn(document.body, "removeChild");

    downloadFile("hello", "report.txt", "text/plain");

    expect(createObjectURLSpy).toHaveBeenCalledTimes(1);
    expect(anchor.href).toBe("blob:mock-url");
    expect(anchor.download).toBe("report.txt");
    expect(appendSpy).toHaveBeenCalledWith(anchor);
    expect(clickSpy).toHaveBeenCalledTimes(1);
    expect(removeSpy).toHaveBeenCalledWith(anchor);
    expect(revokeObjectURLSpy).toHaveBeenCalledWith("blob:mock-url");

    createObjectURLSpy.mockRestore();
    revokeObjectURLSpy.mockRestore();
    createElementSpy.mockRestore();
    appendSpy.mockRestore();
    removeSpy.mockRestore();
    clickSpy.mockRestore();
  });
});
