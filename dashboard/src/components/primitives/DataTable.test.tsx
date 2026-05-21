import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ColumnDef } from "@tanstack/react-table";
import { DataTable, VIRTUALIZE_THRESHOLD, type DataTableProps, type DecisionTier } from "./DataTable";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface Row {
  id: string;
  name: string;
  decision?: DecisionTier;
}

const baseColumns: ColumnDef<Row, unknown>[] = [
  {
    accessorKey: "name",
    header: "Name",
    enableSorting: true,
  },
  {
    accessorKey: "id",
    header: "ID",
    enableSorting: false,
  },
];

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
  act(() => {
    root.render(React.createElement(DataTable<T>, props as DataTableProps<T>));
  });
}

function bodyRows(): NodeListOf<HTMLTableRowElement> {
  return container.querySelectorAll<HTMLTableRowElement>("tbody > tr");
}

describe("primitives/DataTable", () => {
  it("renders all rows from data when below virtualization threshold", () => {
    const data: Row[] = [
      { id: "r1", name: "Alpha" },
      { id: "r2", name: "Bravo" },
      { id: "r3", name: "Charlie" },
    ];
    render<Row>({ columns: baseColumns, data, emptyState: <div>empty</div> });
    expect(bodyRows().length).toBe(3);
    expect(container.textContent).toContain("Alpha");
    expect(container.textContent).toContain("Bravo");
    expect(container.textContent).toContain("Charlie");
  });

  it("renders the emptyState slot when data is empty", () => {
    render<Row>({
      columns: baseColumns,
      data: [],
      emptyState: <div data-testid="empty-marker">Nothing to see</div>,
    });
    expect(bodyRows().length).toBe(1);
    expect(container.querySelector('[data-testid="empty-marker"]')).not.toBeNull();
    expect(container.textContent).toContain("Nothing to see");
  });

  it("toggles sort asc → desc → unsorted on repeated header click", () => {
    const data: Row[] = [
      { id: "r1", name: "Bravo" },
      { id: "r2", name: "Alpha" },
      { id: "r3", name: "Charlie" },
    ];
    render<Row>({ columns: baseColumns, data, emptyState: <div>empty</div> });

    const nameHeader = container.querySelector<HTMLTableCellElement>("thead th");
    expect(nameHeader).not.toBeNull();

    const namesAfterClick = (): string[] =>
      Array.from(container.querySelectorAll("tbody > tr")).map(
        (tr) => (tr as HTMLElement).querySelector("td")?.textContent ?? "",
      );

    act(() => {
      nameHeader!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(namesAfterClick()).toEqual(["Alpha", "Bravo", "Charlie"]);
    expect(nameHeader!.getAttribute("aria-sort")).toBe("ascending");

    act(() => {
      nameHeader!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(namesAfterClick()).toEqual(["Charlie", "Bravo", "Alpha"]);
    expect(nameHeader!.getAttribute("aria-sort")).toBe("descending");

    act(() => {
      nameHeader!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(namesAfterClick()).toEqual(["Bravo", "Alpha", "Charlie"]);
    expect(nameHeader!.getAttribute("aria-sort")).toBe("none");
  });

  it("invokes onRowClick with the original row when a row is clicked", () => {
    const handler = vi.fn();
    const data: Row[] = [
      { id: "r1", name: "Alpha" },
      { id: "r2", name: "Bravo" },
    ];
    render<Row>({ columns: baseColumns, data, emptyState: <div>empty</div>, onRowClick: handler });

    const firstRow = container.querySelector<HTMLTableRowElement>("tbody > tr");
    expect(firstRow).not.toBeNull();
    act(() => {
      firstRow!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler).toHaveBeenCalledWith(data[0]);
  });

  it("does NOT fire onRowClick when the click originates inside an interactive child", () => {
    const handler = vi.fn();
    const data: Row[] = [{ id: "r1", name: "Alpha" }];
    const cols: ColumnDef<Row, unknown>[] = [
      {
        accessorKey: "name",
        header: "Name",
        cell: () => <button data-testid="row-action">Act</button>,
      },
    ];
    render<Row>({ columns: cols, data, emptyState: <div>empty</div>, onRowClick: handler });

    const button = container.querySelector<HTMLButtonElement>('[data-testid="row-action"]');
    expect(button).not.toBeNull();
    act(() => {
      button!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(handler).not.toHaveBeenCalled();
  });

  it("applies the decision-identity left edge per row via decisionAccessor", () => {
    const data: Row[] = [
      { id: "r1", name: "Alpha", decision: "allow" },
      { id: "r2", name: "Bravo", decision: "deny" },
      { id: "r3", name: "Charlie", decision: "require_approval" },
    ];
    render<Row>({
      columns: baseColumns,
      data,
      emptyState: <div>empty</div>,
      decisionAccessor: (row) => row.decision,
    });

    const rows = bodyRows();
    expect(rows.length).toBe(3);
    expect(rows[0].getAttribute("data-decision-tier")).toBe("allow");
    expect(rows[0].style.boxShadow).toContain("var(--color-success)");
    expect(rows[1].getAttribute("data-decision-tier")).toBe("deny");
    expect(rows[1].style.boxShadow).toContain("var(--color-danger)");
    expect(rows[2].getAttribute("data-decision-tier")).toBe("require_approval");
    expect(rows[2].style.boxShadow).toContain("var(--color-warning)");
  });

  it("activates virtualization above the threshold and renders far fewer than data.length <tr>s", () => {
    const data: Row[] = Array.from({ length: 1000 }, (_, i) => ({
      id: `r${i}`,
      name: `Row ${i}`,
    }));
    render<Row>({
      columns: baseColumns,
      data,
      emptyState: <div>empty</div>,
      virtualizedHeight: 480,
      estimatedRowHeight: 44,
    });

    const scrollContainer = container.querySelector<HTMLDivElement>('[data-virtualized="true"]');
    expect(scrollContainer).not.toBeNull();
    expect(VIRTUALIZE_THRESHOLD).toBe(100);

    const rendered = bodyRows().length;
    expect(rendered).toBeLessThan(50);
  });

  it("can opt out of virtualization above the threshold for page-scrolled tables", () => {
    const data: Row[] = Array.from({ length: 125 }, (_, i) => ({
      id: `r${i}`,
      name: `Row ${i}`,
    }));
    render<Row>({
      columns: baseColumns,
      data,
      emptyState: <div>empty</div>,
      disableVirtualization: true,
    });

    expect(container.querySelector('[data-virtualized="true"]')).toBeNull();
    expect(bodyRows().length).toBe(125);
  });

  it("does NOT activate virtualization at the threshold boundary (data.length === 100)", () => {
    const data: Row[] = Array.from({ length: 100 }, (_, i) => ({
      id: `r${i}`,
      name: `Row ${i}`,
    }));
    render<Row>({ columns: baseColumns, data, emptyState: <div>empty</div> });

    expect(container.querySelector('[data-virtualized="true"]')).toBeNull();
    expect(bodyRows().length).toBe(100);
  });
});
