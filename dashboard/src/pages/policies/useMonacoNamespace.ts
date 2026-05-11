import { useEffect, useState } from "react";

// Monaco language-service registrar surface used by RuleMonacoEditor's
// schema diagnostics wiring. Typed with the minimum API actually consumed
// to avoid pulling in the heavy `monaco-editor` types.
export interface MonacoCompletionContext {
  triggerKind?: number;
  triggerCharacter?: string;
}

export interface MonacoCompletionItem {
  label: string;
  kind: number;
  insertText: string;
  documentation?: string;
  range?: unknown;
}

export interface MonacoTextModel {
  getWordAtPosition(position: { lineNumber: number; column: number }): { word: string; startColumn: number; endColumn: number } | null;
  uri: { toString(): string };
}

export interface MonacoMarker {
  severity: number;
  message: string;
  startLineNumber: number;
  startColumn: number;
  endLineNumber: number;
  endColumn: number;
}

export interface MonacoNamespace {
  MarkerSeverity: { Error: number; Warning: number; Info: number; Hint: number };
  languages: {
    CompletionItemKind: { Property: number; EnumMember: number };
    registerCompletionItemProvider(
      languageId: string,
      provider: {
        triggerCharacters?: string[];
        provideCompletionItems(
          model: MonacoTextModel,
          position: { lineNumber: number; column: number },
          context: MonacoCompletionContext,
        ): { suggestions: MonacoCompletionItem[] };
      },
    ): { dispose(): void };
    registerHoverProvider(
      languageId: string,
      provider: {
        provideHover(
          model: MonacoTextModel,
          position: { lineNumber: number; column: number },
        ): { contents: Array<{ value: string }> } | null;
      },
    ): { dispose(): void };
  };
  editor: {
    setModelMarkers(
      model: MonacoTextModel,
      owner: string,
      markers: MonacoMarker[],
    ): void;
    getModels(): MonacoTextModel[];
  };
}

/**
 * Resolves the Monaco namespace (`monaco` global) once `@monaco-editor/react`
 * has finished loading the editor. Returns null while loading or when the
 * test stub is active. Wrapping the loader behind this hook keeps tests
 * stable: vitest aliases `@monaco-editor/react` to a stub that doesn't
 * export `useMonaco`, so we never reach the loader and the consumer's
 * provider-registration effect short-circuits cleanly.
 */
export function useMonacoNamespace(): MonacoNamespace | null {
  const [monaco, setMonaco] = useState<MonacoNamespace | null>(null);

  useEffect(() => {
    let cancelled = false;
    let didImport = false;

    (async () => {
      try {
        // The dynamic import runs only in the browser path; the vitest
        // alias replaces this module with a stub that has no `loader`
        // export, in which case we just leave `monaco` as null and the
        // schema-provider effect no-ops. That preserves the existing
        // RuleMonacoEditor.test.tsx contract.
        const monacoReact: unknown = await import("@monaco-editor/react");
        if (cancelled) return;
        const candidate = monacoReact as { loader?: { init?: () => Promise<unknown> } };
        if (typeof candidate?.loader?.init !== "function") return;
        didImport = true;
        const loaded = await candidate.loader.init();
        if (cancelled) return;
        setMonaco(loaded as MonacoNamespace);
      } catch {
        if (!cancelled && didImport) {
          // Initialization failed after the loader started — leave monaco
          // null so the consumer effect no-ops; the editor still renders
          // (vite already lazy-loaded the React wrapper).
          setMonaco(null);
        }
      }
    })();

    return () => {
      cancelled = true;
    };
  }, []);

  return monaco;
}
