import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { DetailList } from "./DetailList";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function renderDetailList(action?: () => void) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <DetailList
        items={[
          {
            label: "Endpoint URL",
            value: "https://gateway.cordum.test/scim",
            mono: true,
            align: "left",
            action: action ? <button type="button" onClick={action}>Copy</button> : undefined,
          },
          {
            label: "Provisioning state",
            value: "Ready",
          },
        ]}
      />,
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

describe("DetailList", () => {
  it("renders mixed detail rows and preserves row actions", () => {
    const onCopy = vi.fn();
    const { container, cleanup } = renderDetailList(onCopy);

    try {
      expect(container.textContent).toContain("Endpoint URL");
      expect(container.textContent).toContain("https://gateway.cordum.test/scim");
      expect(container.textContent).toContain("Provisioning state");
      expect(container.textContent).toContain("Ready");

      const button = container.querySelector("button");
      expect(button?.textContent).toBe("Copy");

      act(() => {
        button?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
      });

      expect(onCopy).toHaveBeenCalledTimes(1);
    } finally {
      cleanup();
    }
  });
});
