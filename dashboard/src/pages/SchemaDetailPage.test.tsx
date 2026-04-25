import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SchemaCreateForm } from "./SchemaDetailPage";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const { hookState } = vi.hoisted(() => ({
  hookState: {
    registerSchema: {
      mutate: vi.fn(),
      isPending: false,
    },
  },
}));

vi.mock("@/hooks/useSchemas", () => ({
  useRegisterSchema: () => hookState.registerSchema,
}));

function renderForm() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);

  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/schemas/new"]}>
        <SchemaCreateForm />
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

function click(element: Element | null) {
  if (!element) throw new Error("Expected element to exist before clicking");
  act(() => {
    element.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
  });
}

async function waitFor(assertion: () => void, timeoutMs = 2000) {
  const start = Date.now();
  while (true) {
    try {
      assertion();
      return;
    } catch (error) {
      if (Date.now() - start >= timeoutMs) {
        throw error;
      }
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10));
      });
    }
  }
}

describe("SchemaCreateForm validation", () => {
  beforeEach(() => {
    hookState.registerSchema = {
      mutate: vi.fn(),
      isPending: false,
    };
  });

  it("renders validation messages for invalid schema input", async () => {
    const { container, cleanup } = renderForm();
    try {
      click(
        Array.from(container.querySelectorAll("button")).find((button) =>
          button.textContent?.includes("Create Schema"),
        ) ?? null,
      );

      await waitFor(() => {
        expect(container.textContent).toContain("Validation issues");
      });
      expect(container.textContent).toContain("Validation issues");
      expect(container.textContent).toContain("Schema name is required");
      expect(container.textContent).toContain("Field name is required");
    } finally {
      cleanup();
    }
  });
});
