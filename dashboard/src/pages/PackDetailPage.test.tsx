import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import PackDetailPage from "./PackDetailPage";

function MockPackDetail({ packId }: { packId: string }) {
  return <div data-pack-id={packId}>pack detail {packId}</div>;
}

vi.mock("@/components/packs/PackDetail", () => ({
  default: MockPackDetail,
}));

function renderPackDetailRoute(initialEntry: string) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route path="/packs/:id?" element={<PackDetailPage />} />
        </Routes>
      </MemoryRouter>,
    );
  });

  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("PackDetailPage", () => {
  it("shows an explicit error state when the route is missing a pack ID", () => {
    const { container, cleanup } = renderPackDetailRoute("/packs");
    try {
      expect(container.textContent).toContain("Pack not found");
      expect(container.textContent).toContain("missing a pack ID");
      expect(container.textContent).toContain("Back to packs");
    } finally {
      cleanup();
    }
  });

  it("renders the pack detail view when an ID is present", () => {
    const { container, cleanup } = renderPackDetailRoute("/packs/demo-pack");
    try {
      expect(container.textContent).toContain("pack detail demo-pack");
      expect(
        container.querySelector("[data-pack-id='demo-pack']"),
      ).not.toBeNull();
    } finally {
      cleanup();
    }
  });
});
