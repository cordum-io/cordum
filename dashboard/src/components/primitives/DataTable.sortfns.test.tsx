import { afterEach, beforeEach, describe, expect, it } from "vitest";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { DataTable, type DataTableProps } from "./DataTable";

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

function render<T>(props: DataTableProps<T>) {
  act(() => root.render(React.createElement(DataTable<T>, props)));
}

function bodyValues(): string[] {
  return Array.from(container.querySelectorAll("tbody > tr")).map(
    (row) => row.querySelector("td")?.textContent ?? "",
  );
}

function header(): HTMLTableCellElement {
  const element = container.querySelector<HTMLTableCellElement>("thead th");
  if (!element) throw new Error("Expected a sortable column header");
  return element;
}

function click(element: Element) {
  act(() => element.dispatchEvent(new MouseEvent("click", { bubbles: true })));
}

describe("primitives/DataTable stock sortFns", () => {
  it("sorts digit-containing strings in natural order", () => {
    render<{ name: string }>({
      columns: [{ accessorKey: "name", header: "Name", enableSorting: true }],
      data: ["item10", "item2", "item1"].map((name) => ({ name })),
      emptyState: <div>empty</div>,
      initialSorting: [{ id: "name", desc: false }],
    });

    const names = bodyValues();
    expect(names).toEqual(["item1", "item2", "item10"]);
    expect(names.indexOf("item2")).toBeLessThan(names.indexOf("item10"));
  });

  it("sorts strings in case-insensitive order", () => {
    render<{ name: string }>({
      columns: [{ accessorKey: "name", header: "Name", enableSorting: true }],
      data: ["Bravo", "alpha", "Charlie", "delta"].map((name) => ({ name })),
      emptyState: <div>empty</div>,
      initialSorting: [{ id: "name", desc: false }],
    });

    expect(bodyValues()).toEqual(["alpha", "Bravo", "Charlie", "delta"]);
  });

  it("cycles numeric sorting descending → ascending → unsorted", () => {
    render<{ value: number }>({
      columns: [{ accessorKey: "value", header: "Value", enableSorting: true }],
      data: [2, 10, 1].map((value) => ({ value })),
      emptyState: <div>empty</div>,
    });

    expect(header().getAttribute("aria-sort")).toBe("none");
    expect(bodyValues().map(Number)).toEqual([2, 10, 1]);

    click(header());
    expect(header().getAttribute("aria-sort")).toBe("descending");
    expect(bodyValues().map(Number)).toEqual([10, 2, 1]);

    click(header());
    expect(header().getAttribute("aria-sort")).toBe("ascending");
    expect(bodyValues().map(Number)).toEqual([1, 2, 10]);

    click(header());
    expect(header().getAttribute("aria-sort")).toBe("none");
    expect(bodyValues().map(Number)).toEqual([2, 10, 1]);
  });
});
