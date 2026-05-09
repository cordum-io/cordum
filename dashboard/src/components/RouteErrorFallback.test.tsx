import { describe, it, expect, vi } from "vitest";
import { fireEvent } from "@testing-library/react";
import { renderWithProviders } from "../test-utils/render";
import { RouteErrorFallback } from "./RouteErrorFallback";

describe("RouteErrorFallback", () => {
  it("renders the route-scoped title and the error message (axe-clean)", async () => {
    const { getByText } = await renderWithProviders(
      <RouteErrorFallback
        route="Jobs"
        error={new Error("Boom in JobsPage")}
        reset={() => {}}
      />,
      { runAxe: true },
    );
    expect(getByText("Couldn't load Jobs")).toBeTruthy();
    expect(getByText("Boom in JobsPage")).toBeTruthy();
  });

  it("clicking Retry invokes reset exactly once", async () => {
    const reset = vi.fn();
    const { getByText } = await renderWithProviders(
      <RouteErrorFallback
        route="Approvals"
        error={new Error("oops")}
        reset={reset}
      />,
    );
    fireEvent.click(getByText("Retry"));
    expect(reset).toHaveBeenCalledTimes(1);
  });

  it("renders a Report bug mailto link with the route + error message in the body", async () => {
    const { getByText } = await renderWithProviders(
      <RouteErrorFallback
        route="Audit log"
        error={new Error("transform failure")}
        reset={() => {}}
      />,
    );
    const link = getByText("Report bug").closest("a") as HTMLAnchorElement | null;
    expect(link).not.toBeNull();
    const href = link?.getAttribute("href") ?? "";
    expect(href.startsWith("mailto:")).toBe(true);
    expect(decodeURIComponent(href)).toContain("Route: Audit log");
    expect(decodeURIComponent(href)).toContain("Error: transform failure");
  });

  it("falls back to a generic message when error.message is empty", async () => {
    const { getByText } = await renderWithProviders(
      <RouteErrorFallback
        route="Topics"
        error={new Error("")}
        reset={() => {}}
      />,
    );
    expect(getByText(/An unexpected error occurred/i)).toBeTruthy();
  });
});
