import { describe, it, expect } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { NuqsTestingAdapter } from "nuqs/adapters/testing";
import { renderWithProviders } from "@/test-utils/render";
import { DecisionsFilterBar } from "./DecisionsFilterBar";

// D8b ships the `Live ●` and `Charts ▾` toggles in DecisionsFilterBar.
// Both wire to nuqs URL state — `?live=on` and `?charts=on` respectively —
// so deep-links from emails / dashboards / Slack open the filter bar in
// the right mode. The actual stream wiring lives in DecisionsPage; this
// suite asserts only the filter-bar's URL roundtrip + visible state.

describe("DecisionsFilterBar (D8b — Live + Charts toggles)", () => {
  it("renders Live toggle with default-off state", async () => {
    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsFilterBar />
      </NuqsTestingAdapter>,
    );
    const liveToggle = await screen.findByRole("switch", { name: /live/i });
    expect(liveToggle.getAttribute("aria-checked")).toBe("false");
  });

  it("renders Charts toggle with default-off state", async () => {
    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsFilterBar />
      </NuqsTestingAdapter>,
    );
    const chartsToggle = await screen.findByRole("switch", { name: /charts/i });
    expect(chartsToggle.getAttribute("aria-checked")).toBe("false");
  });

  it("flipping Live writes ?live=on to URL state", async () => {
    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsFilterBar />
      </NuqsTestingAdapter>,
    );
    const liveToggle = await screen.findByRole("switch", { name: /live/i });
    fireEvent.click(liveToggle);
    await waitFor(() =>
      expect(liveToggle.getAttribute("aria-checked")).toBe("true"),
    );
  });

  it("flipping Charts writes ?charts=on to URL state", async () => {
    renderWithProviders(
      <NuqsTestingAdapter searchParams="">
        <DecisionsFilterBar />
      </NuqsTestingAdapter>,
    );
    const chartsToggle = await screen.findByRole("switch", { name: /charts/i });
    fireEvent.click(chartsToggle);
    await waitFor(() =>
      expect(chartsToggle.getAttribute("aria-checked")).toBe("true"),
    );
  });

  it("Live toggle reads initial ?live=on URL state on mount", async () => {
    renderWithProviders(
      <NuqsTestingAdapter searchParams="?live=on">
        <DecisionsFilterBar />
      </NuqsTestingAdapter>,
    );
    const liveToggle = await screen.findByRole("switch", { name: /live/i });
    expect(liveToggle.getAttribute("aria-checked")).toBe("true");
  });

  it("Charts toggle reads initial ?charts=on URL state on mount", async () => {
    renderWithProviders(
      <NuqsTestingAdapter searchParams="?charts=on">
        <DecisionsFilterBar />
      </NuqsTestingAdapter>,
    );
    const chartsToggle = await screen.findByRole("switch", { name: /charts/i });
    expect(chartsToggle.getAttribute("aria-checked")).toBe("true");
  });
});
