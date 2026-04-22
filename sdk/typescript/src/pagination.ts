export type PaginatedEnvelope<T> = {
  items?: readonly T[] | T[] | undefined;
  nextCursor?: string | null | undefined;
  next_cursor?: string | null | undefined;
};

export async function* paginate<TItem, TPage extends PaginatedEnvelope<TItem>>(
  firstCall: (signal: AbortSignal) => Promise<TPage>,
  extractCursor: (page: TPage) => string | null | undefined,
  nextCallWithCursor: (cursor: string, signal: AbortSignal) => Promise<TPage>,
): AsyncGenerator<TItem> {
  const controller = new AbortController();

  try {
    let page = await firstCall(controller.signal);

    while (true) {
      for (const item of page.items ?? []) {
        yield item;
      }

      const cursor = extractCursor(page);
      if (!cursor) {
        return;
      }

      page = await nextCallWithCursor(cursor, controller.signal);
    }
  } finally {
    controller.abort(new DOMException("Pagination closed", "AbortError"));
  }
}

export function getNextCursor<TItem>(page: PaginatedEnvelope<TItem>): string | null | undefined {
  return page.nextCursor ?? page.next_cursor;
}
