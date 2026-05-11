import { useCallback, useState } from "react";

export interface UseCopyToClipboardOptions {
  /**
   * Milliseconds to keep `copied: true` after a successful write before
   * auto-resetting. Pass 0 to disable the auto-reset (caller manages
   * `copied` lifetime). Default: 1500ms.
   */
  resetMs?: number;
  /**
   * Called after `navigator.clipboard.writeText` resolves successfully.
   * Common usage: emit a toast (`toast.success("Copied")`).
   */
  onSuccess?: () => void;
  /**
   * Called when `navigator.clipboard.writeText` rejects — typically when
   * the browser is in an insecure context or clipboard permission is
   * denied. Common usage: log a structured warning, show a toast.
   */
  onError?: (err: unknown) => void;
}

export interface UseCopyToClipboardResult {
  /** True for `resetMs` after a successful copy; resets to false. */
  copied: boolean;
  /** Write the given string to the system clipboard. Always returns. */
  copy: (value: string) => Promise<void>;
}

/**
 * Shared clipboard-write hook for the dashboard. Replaces the local
 * `useState(false) + try/catch handleCopy` pattern that was duplicated
 * across 17+ files.
 *
 * Failure semantics: by default failures are silently swallowed (consistent
 * with the existing inline implementations) — caller passes `onError` to
 * surface the rejection. Never throws; always returns a settled promise.
 */
export function useCopyToClipboard(
  options: UseCopyToClipboardOptions = {},
): UseCopyToClipboardResult {
  const { resetMs = 1500, onSuccess, onError } = options;
  const [copied, setCopied] = useState(false);

  const copy = useCallback(
    async (value: string) => {
      try {
        await navigator.clipboard.writeText(value);
        setCopied(true);
        onSuccess?.();
        if (resetMs > 0) {
          window.setTimeout(() => setCopied(false), resetMs);
        }
      } catch (err) {
        onError?.(err);
      }
    },
    [resetMs, onSuccess, onError],
  );

  return { copied, copy };
}
