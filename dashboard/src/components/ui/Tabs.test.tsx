import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRoot, type Root } from "react-dom/client";
import { Plug2 } from "lucide-react";
import { Tabs } from "./Tabs";

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

describe("Tabs", () => {
  it("renders an accessible tablist", () => {
    act(() => {
      root.render(
        <Tabs
          ariaLabel="Settings sections"
          activeTab="servers"
          onChange={() => undefined}
          tabs={[
            { id: "servers", label: "Servers" },
            { id: "analytics", label: "Analytics" },
          ]}
        />,
      );
    });

    const tablist = container.querySelector('[role="tablist"]');
    expect(tablist?.getAttribute("aria-label")).toBe("Settings sections");
    expect(container.querySelectorAll('[role="tab"]').length).toBe(2);
  });

  it("renders optional icons and counts", () => {
    act(() => {
      root.render(
        <Tabs
          activeTab="servers"
          onChange={() => undefined}
          tabs={[
            { id: "servers", label: "Servers", icon: <Plug2 className="h-3 w-3" />, count: 1 },
          ]}
        />,
      );
    });

    expect(container.querySelector("svg")).not.toBeNull();
    expect(container.textContent).toContain("1");
  });

  it("supports the segmented variant while preserving pressed state semantics", () => {
    act(() => {
      root.render(
        <Tabs
          variant="segmented"
          activeTab="users"
          onChange={() => undefined}
          tabs={[
            { id: "users", label: "Users", count: 12 },
            { id: "roles", label: "Roles", count: 4 },
          ]}
        />,
      );
    });

    const activeTab = container.querySelector('[role="tab"][aria-selected="true"]');
    expect(activeTab?.textContent).toContain("Users");
    expect(container.textContent).toContain("12");
    expect(container.textContent).toContain("4");
  });

  it("does not invoke onChange for disabled tabs", () => {
    const onChange = vi.fn();
    act(() => {
      root.render(
        <Tabs
          activeTab="servers"
          onChange={onChange}
          tabs={[
            { id: "servers", label: "Servers" },
            { id: "analytics", label: "Analytics", disabled: true },
          ]}
        />,
      );
    });

    const disabledTab = container.querySelectorAll('[role="tab"]')[1] as HTMLButtonElement;
    act(() => disabledTab.click());
    expect(onChange).not.toHaveBeenCalled();
    expect(disabledTab.disabled).toBe(true);
  });
});
