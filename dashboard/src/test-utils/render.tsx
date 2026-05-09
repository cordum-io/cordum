import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, type RenderOptions, type RenderResult } from "@testing-library/react";
import type { ReactElement, ReactNode } from "react";
import { useEffect } from "react";
import { MemoryRouter, type MemoryRouterProps } from "react-router-dom";
import { Toaster } from "sonner";
import { registerQueryClient } from "@/state/config";
import { useUiStore } from "@/state/ui";
import { ensureMswServerListening } from "./msw";

export { fireEvent, screen, waitFor, within } from "@testing-library/dom";
export { cleanup, render } from "@testing-library/react";

export interface RenderWithProvidersOptions extends Omit<RenderOptions, "wrapper"> {
  initialEntries?: MemoryRouterProps["initialEntries"];
  queryClient?: QueryClient;
  /**
   * When true, runs axe-core against the rendered container after the
   * synchronous initial render and throws on critical/serious WCAG 2 AA
   * violations. Default false to preserve existing tests that intentionally
   * render inaccessible states for negative-test purposes. axe-core is
   * dynamic-imported so non-opted tests stay fast.
   */
  runAxe?: boolean;
  /**
   * Theme mode for the axe pass when runAxe is true. Defaults to "light".
   * The helper sets `<html class>` before invoking axe so color-contrast
   * tokens resolve against the right palette. (jsdom doesn't composite
   * backdrop-filter; structural contrast still passes WCAG AA — see
   * test-utils/a11y.ts.)
   */
  axeMode?: "light" | "dark";
}

export interface RenderWithProvidersResult extends RenderResult {
  queryClient: QueryClient;
}

export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        gcTime: 0,
        staleTime: 0,
        refetchOnWindowFocus: false,
      },
      mutations: {
        retry: false,
      },
    },
  });
}

function ThemeSync() {
  const resolvedTheme = useUiStore((s) => s.resolvedTheme);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.remove("light", "dark");
    root.classList.add(resolvedTheme);
    root.style.colorScheme = resolvedTheme;
  }, [resolvedTheme]);

  return null;
}

export function renderWithProviders(
  ui: ReactElement,
  options: RenderWithProvidersOptions & { runAxe: true },
): Promise<RenderWithProvidersResult>;
export function renderWithProviders(
  ui: ReactElement,
  options?: RenderWithProvidersOptions,
): RenderWithProvidersResult;
export function renderWithProviders(
  ui: ReactElement,
  {
    initialEntries = ["/"],
    queryClient = createTestQueryClient(),
    runAxe = false,
    axeMode = "light",
    ...renderOptions
  }: RenderWithProvidersOptions = {},
): RenderWithProvidersResult | Promise<RenderWithProvidersResult> {
  ensureMswServerListening();
  registerQueryClient(queryClient);

  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={initialEntries}>
          <ThemeSync />
          <Toaster
            position="top-right"
            toastOptions={{
              style: {
                background: "var(--surface)",
                color: "var(--text)",
                border: "1px solid var(--border-color)",
                fontFamily: "var(--font-sans)",
              },
            }}
          />
          {children}
        </MemoryRouter>
      </QueryClientProvider>
    );
  }

  const rendered = render(ui, { wrapper: Wrapper, ...renderOptions });
  const result: RenderWithProvidersResult = { queryClient, ...rendered };

  if (!runAxe) return result;

  return import("./a11y").then(async ({ assertNoSeriousAxeViolations }) => {
    await assertNoSeriousAxeViolations(result.container, { mode: axeMode });
    return result;
  });
}
