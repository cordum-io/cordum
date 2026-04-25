import { describe, expect, it } from "vitest";
import buttonSource from "./Button.tsx?raw";
import cardSource from "./Card.tsx?raw";
import tabsSource from "./Tabs.tsx?raw";

const softDurationPattern = /duration-\[var\(--duration-soft\)\]/;

describe("Soft UI primitives", () => {
  it("keeps Button size variants rounded-xl with the shared soft duration token", () => {
    expect(buttonSource).toMatch(/sm:\s*"[^"]*rounded-xl/);
    expect(buttonSource).toMatch(/md:\s*"[^"]*rounded-xl/);
    expect(buttonSource).toMatch(/lg:\s*"[^"]*rounded-xl/);
    expect(buttonSource).toMatch(/icon:\s*"[^"]*rounded-xl/);
    expect(buttonSource).toMatch(softDurationPattern);
  });

  it("keeps Card on rounded-xl with the shared soft duration token", () => {
    expect(cardSource).toMatch(/rounded-xl/);
    expect(cardSource).toMatch(softDurationPattern);
  });

  it("keeps Tabs on compact rounded-xl styling with the shared soft duration token", () => {
    expect(tabsSource).toMatch(/rounded-xl/);
    expect(tabsSource).toMatch(softDurationPattern);
  });
});
