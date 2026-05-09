import { describe, expect, it } from "vitest";
import { renderWithProviders } from "./render";

describe("renderWithProviders runAxe opt-in", () => {
  it("returns synchronously and skips axe when runAxe is omitted", () => {
    const result = renderWithProviders(<button>Save</button>);
    expect(typeof (result as { then?: unknown }).then).toBe("undefined");
    expect(result.container.querySelector("button")?.textContent).toBe("Save");
  });

  it("returns a Promise that resolves on a clean render when runAxe is true", async () => {
    const result = await renderWithProviders(
      <button type="button">Save</button>,
      { runAxe: true },
    );

    expect(result.container.querySelector("button")?.textContent).toBe("Save");
  });

  it("throws on a critical violation (img without alt) when runAxe is true", async () => {
    // The img-without-alt is the deliberate violation under test; jsx-a11y
    // would correctly flag it on a production path, but here it's the
    // negative fixture proving the runtime gate fires.
    await expect(
      renderWithProviders(
        // eslint-disable-next-line jsx-a11y/alt-text
        <img src="x.jpg" />,
        { runAxe: true },
      ),
    ).rejects.toThrow(/Axe violations \(any-impact gate\)/);
  });

  it("respects axeMode dark and applies the dark class to documentElement", async () => {
    await renderWithProviders(<button type="button">Toggle</button>, {
      runAxe: true,
      axeMode: "dark",
    });

    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });
});
