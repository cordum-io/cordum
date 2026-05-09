import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test-utils/render";
import NotFoundPage from "./NotFoundPage";

// Phase 5a (task-bf55ddbd) — page-level demo for the renderWithProviders
// `runAxe: true` opt-in. NotFoundPage is the canonical page test for the
// strict gate because its render is fully synchronous (no async data hooks),
// so the post-render axe pass exercises the actual customer DOM rather
// than a loading skeleton.
describe("NotFoundPage", () => {
  it("renders the 404 surface with no WCAG 2 A/AA violations (axe-clean)", async () => {
    const { container } = await renderWithProviders(<NotFoundPage />, {
      initialEntries: ["/missing-route"],
      runAxe: true,
    });

    expect(screen.getByRole("heading", { name: /page not found/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /go back/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /dashboard/i })).toBeTruthy();
    expect(container.querySelector("h1")).toBeTruthy();
  });
});
