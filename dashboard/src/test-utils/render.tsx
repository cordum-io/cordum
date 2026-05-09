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
   * synchronous initial render and throws on **any** WCAG 2 A/AA violation
   * (no impact filter). The only rule disabled is `color-contrast`, because
   * jsdom doesn't composite backdrop-filter and would false-positive on
   * glass-panel surfaces; Lighthouse CI / Phase 5b is the canonical
   * color-contrast gate. Default false to preserve existing tests that
   * intentionally render inaccessible states for negative-test purposes.
   * axe-core is dynamic-imported so non-opted tests stay fast.
   */
  runAxe?: boolean;
  /**
   * Theme mode for the axe pass when runAxe is true. Defaults to "light".
   * Sets `<html class>` before invoking axe so color-tokens resolve
   * against the right palette (even though color-contrast is disabled,
   * this keeps the rendered tree consistent with what users see).
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

  return runAxeStrict(result.container, axeMode).then(() => result);
}

// Strict axe gate per Phase 5a DoD #1: throws on ANY WCAG 2 A/AA violation
// in the rendered container. Only color-contrast is disabled (jsdom can't
// composite backdrop-filter; Lighthouse CI / Phase 5b owns color-contrast).
// Imports axe-core dynamically so non-opted tests don't pay the load cost.
async function runAxeStrict(
  container: HTMLElement,
  mode: "light" | "dark",
): Promise<void> {
  const root = document.documentElement;
  root.classList.remove("light", "dark");
  root.classList.add(mode);

  const axe = (await import("axe-core")).default;
  const results = await axe.run(container, {
    runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] },
    rules: { "color-contrast": { enabled: false } },
  });

  if (results.violations.length === 0) return;

  const summary = results.violations
    .map((v) => {
      const impact = v.impact ?? "unknown";
      const nodeDetail = v.nodes
        .slice(0, 3)
        .map(
          (n) =>
            `      target=${n.target.join(",")} | failure=${n.failureSummary?.split("\n").join(" / ")}`,
        )
        .join("\n");
      return `  ${v.id} (${impact}): ${v.description} — ${v.nodes.length} node(s)\n${nodeDetail}`;
    })
    .join("\n");
  throw new Error(`Axe violations (any-impact gate):\n${summary}`);
}
