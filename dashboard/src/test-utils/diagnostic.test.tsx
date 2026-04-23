import { useQuery } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { lastConsoleErrorDiagnostic, reportQueryClientDiagnostic } from "./setup";

function QueryWithoutProvider() {
  useQuery({ queryKey: ["missing-provider"], queryFn: () => "never" });
  return <div>never renders</div>;
}

describe("QueryClient diagnostic", () => {
  it("prints the renderWithProviders ADR diagnostic and still propagates the error", () => {
    const capturedErrorCalls: string[] = [];
    const probe = vi.spyOn(console, "error").mockImplementation((...args) => {
      capturedErrorCalls.push(args.map(String).join("\n"));
    });

    try {
      expect(() => render(<QueryWithoutProvider />)).toThrow(/No QueryClient set/);
      reportQueryClientDiagnostic("No QueryClient set, use QueryClientProvider to set one");
      expect(lastConsoleErrorDiagnostic).toContain("renderWithProviders from");
    } finally {
      probe.mockRestore();
    }
  });
});
