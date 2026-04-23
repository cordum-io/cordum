import { afterAll, afterEach, beforeAll, vi } from "vitest";
import { closeMswServer, resetMswServer } from "./msw";

const queryClientErrorPattern = /No QueryClient set.*use QueryClientProvider/;
const queryClientDiagnostic = [
  "✗ Test error: React component rendered outside a QueryClient.",
  "  Fix: use renderWithProviders from 'src/test-utils/render'.",
  "  See ADR: dashboard/docs/adr/0001-page-test-providers.md",
].join("\n");

let lastConsoleErrorDiagnostic = "";

function reportQueryClientDiagnostic(value: unknown): boolean {
  const rendered = value instanceof Error ? value.message : String(value);
  if (queryClientErrorPattern.test(rendered)) {
    lastConsoleErrorDiagnostic = queryClientDiagnostic;
    console.error(queryClientDiagnostic);
    return true;
  }
  return false;
}

let consoleErrorSpy: ReturnType<typeof vi.spyOn>;
let originalConsoleError: typeof console.error;

const onWindowError = (event: ErrorEvent) => {
  reportQueryClientDiagnostic(event.error ?? event.message);
};

beforeAll(() => {
  originalConsoleError = console.error.bind(console);
  consoleErrorSpy = vi.spyOn(console, "error").mockImplementation((...args: Parameters<typeof console.error>) => {
    originalConsoleError(...args);
    reportQueryClientDiagnostic(args.map((arg) => (arg instanceof Error ? arg.message : String(arg))).join("\n"));
  });

  window.addEventListener("error", onWindowError);
});

afterEach(() => {
  resetMswServer();
  lastConsoleErrorDiagnostic = "";
});

afterAll(() => {
  window.removeEventListener("error", onWindowError);
  closeMswServer();
  consoleErrorSpy?.mockRestore();
});

export { lastConsoleErrorDiagnostic, queryClientDiagnostic, reportQueryClientDiagnostic };
