import React, { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { Activity } from "lucide-react";
import { StatTile } from "./StatTile";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

describe("StatTile", () => {
  it("renders a metric label, value, helper text, and icon", () => {
    act(() => {
      root.render(
        <StatTile
          label="Servers"
          value={3}
          helperText="2 connected"
          icon={<Activity className="h-4 w-4" />}
          accent="healthy"
        />,
      );
    });

    expect(container.textContent).toContain("Servers");
    expect(container.textContent).toContain("3");
    expect(container.textContent).toContain("2 connected");
    expect(container.querySelector("svg")).not.toBeNull();
  });
});
