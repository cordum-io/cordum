import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { DataTableColumn, DataTableSorting } from "./DataTable";
import { DataTable, VIRTUALIZE_THRESHOLD, type DataTableProps, type DecisionTier } from "./DataTable";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface Row {
  id: string;
  name: string;
  decision?: DecisionTier;
}

const baseColumns: DataTableColumn<Row>[] = [
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
    const cols: DataTableColumn<Row>[] = [
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

// Held-out characterization tests (task-722f3b29): pin user-visible v8 behavior
// that the TanStack v9 migration (task-fa2b4cef) must preserve unchanged.
describe("primitives/DataTable characterization", () => {
  const unsorted: Row[] = [
    { id: "r1", name: "Bravo" },
    { id: "r2", name: "Alpha" },
    { id: "r3", name: "Charlie" },
  ];

  const headers = (): HTMLTableCellElement[] =>
    Array.from(container.querySelectorAll<HTMLTableCellElement>("thead th"));

  const firstColumnText = (): string[] =>
    Array.from(bodyRows()).map((tr) => tr.querySelector("td")?.textContent ?? "");

  const click = (el: HTMLElement) =>
    act(() => {
      el.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

  const press = (el: HTMLElement, key: string): KeyboardEvent => {
    const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
    act(() => {
      el.dispatchEvent(event);
    });
    return event;
  };

  it("cycles aria-sort none → ascending → descending → none on header clicks, with matching row order", () => {
    render<Row>({ columns: baseColumns, data: unsorted, emptyState: <div>empty</div> });
    const nameHeader = headers()[0];

    expect(nameHeader.getAttribute("aria-sort")).toBe("none");
    expect(firstColumnText()).toEqual(["Bravo", "Alpha", "Charlie"]);

    click(nameHeader);
    expect(nameHeader.getAttribute("aria-sort")).toBe("ascending");
    expect(firstColumnText()).toEqual(["Alpha", "Bravo", "Charlie"]);

    click(nameHeader);
    expect(nameHeader.getAttribute("aria-sort")).toBe("descending");
    expect(firstColumnText()).toEqual(["Charlie", "Bravo", "Alpha"]);

    click(nameHeader);
    expect(nameHeader.getAttribute("aria-sort")).toBe("none");
    expect(firstColumnText()).toEqual(["Bravo", "Alpha", "Charlie"]);
  });

  it("toggles sorting identically with Enter and Space on a focusable sortable header", () => {
    render<Row>({ columns: baseColumns, data: unsorted, emptyState: <div>empty</div> });
    const nameHeader = headers()[0];
    expect(nameHeader.tabIndex).toBe(0);
    expect(nameHeader.getAttribute("tabindex")).toBe("0");

    const enter = press(nameHeader, "Enter");
    expect(enter.defaultPrevented).toBe(true);
    expect(nameHeader.getAttribute("aria-sort")).toBe("ascending");
    expect(firstColumnText()).toEqual(["Alpha", "Bravo", "Charlie"]);

    const space = press(nameHeader, " ");
    expect(space.defaultPrevented).toBe(true);
    expect(nameHeader.getAttribute("aria-sort")).toBe("descending");
    expect(firstColumnText()).toEqual(["Charlie", "Bravo", "Alpha"]);

    press(nameHeader, "Enter");
    expect(nameHeader.getAttribute("aria-sort")).toBe("none");
    expect(firstColumnText()).toEqual(["Bravo", "Alpha", "Charlie"]);

    const other = press(nameHeader, "a");
    expect(other.defaultPrevented).toBe(false);
    expect(nameHeader.getAttribute("aria-sort")).toBe("none");
  });

  it("gives an enableSorting:false column no aria-sort, no tabIndex, and no reorder on click or key", () => {
    render<Row>({ columns: baseColumns, data: unsorted, emptyState: <div>empty</div> });
    const idHeader = headers()[1];
    expect(idHeader.textContent).toBe("ID");
    expect(idHeader.hasAttribute("aria-sort")).toBe(false);
    expect(idHeader.hasAttribute("tabindex")).toBe(false);

    click(idHeader);
    press(idHeader, "Enter");
    expect(idHeader.hasAttribute("aria-sort")).toBe(false);
    expect(firstColumnText()).toEqual(["Bravo", "Alpha", "Charlie"]);
  });

  it("applies initialSorting on first paint with the matching aria-sort", () => {
    const initialSorting: DataTableSorting = [{ id: "name", desc: true }];
    render<Row>({ columns: baseColumns, data: unsorted, emptyState: <div>empty</div>, initialSorting });
    const nameHeader = headers()[0];

    expect(nameHeader.getAttribute("aria-sort")).toBe("descending");
    expect(firstColumnText()).toEqual(["Charlie", "Bravo", "Alpha"]);
  });

  it("right-aligns both the header and the cells of a meta.align:right column", () => {
    const cols: DataTableColumn<Row>[] = [
      { accessorKey: "name", header: "Name" },
      { accessorKey: "id", header: "ID", meta: { align: "right" } },
    ];
    render<Row>({ columns: cols, data: unsorted, emptyState: <div>empty</div> });
    const [nameHeader, idHeader] = headers();

    expect(idHeader.classList.contains("text-right")).toBe(true);
    expect(nameHeader.classList.contains("text-right")).toBe(false);
    for (const tr of Array.from(bodyRows())) {
      const [nameCell, idCell] = Array.from(tr.querySelectorAll("td"));
      expect(idCell.classList.contains("text-right")).toBe(true);
      expect(nameCell.classList.contains("text-right")).toBe(false);
    }
  });

  it("renders a size:200 column header at width 200px and leaves default-size headers unstyled", () => {
    const cols: DataTableColumn<Row>[] = [
      { accessorKey: "name", header: "Name", size: 200 },
      { accessorKey: "id", header: "ID" },
    ];
    render<Row>({ columns: cols, data: unsorted, emptyState: <div>empty</div> });
    const [sizedHeader, defaultHeader] = headers();

    expect(sizedHeader.style.width).toBe("200px");
    expect(defaultHeader.style.width).toBe("");
  });

  it("renders function header and cell renderers with row.original values", () => {
    const cols: DataTableColumn<Row>[] = [
      {
        id: "combined",
        header: () => <span data-testid="fn-header">H</span>,
        cell: ({ row }) => <span data-testid="fn-cell">{`${row.original.id}:${row.original.name}`}</span>,
      },
    ];
    render<Row>({ columns: cols, data: unsorted, emptyState: <div>empty</div> });

    const header = container.querySelector('[data-testid="fn-header"]');
    expect(header?.textContent).toBe("H");
    expect(headers()[0].contains(header)).toBe(true);
    const cells = Array.from(container.querySelectorAll('[data-testid="fn-cell"]')).map((el) => el.textContent);
    expect(cells).toEqual(["r1:Bravo", "r2:Alpha", "r3:Charlie"]);
  });
});
