// ---------------------------------------------------------------------------
// Structured logger aligned with backend slog format
// ---------------------------------------------------------------------------

type LogLevel = "debug" | "info" | "warn" | "error";

interface LogEntry {
  ts: string;
  level: LogLevel;
  component: string;
  msg: string;
  fields?: Record<string, unknown>;
}

const LEVEL_ORDER: Record<LogLevel, number> = {
  debug: 0,
  info: 1,
  warn: 2,
  error: 3,
};

const LEVEL_FN: Record<LogLevel, "log" | "warn" | "error"> = {
  debug: "log",
  info: "log",
  warn: "warn",
  error: "error",
};

function resolveDebugCategories(): Set<string> | null {
  const raw = import.meta.env.VITE_DEBUG_CATEGORIES as string | undefined;
  if (!raw || !raw.trim()) return null; // null = all categories enabled
  const cats = raw
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
  return cats.length > 0 ? new Set(cats) : null;
}

function createLogger(minLevel: LogLevel) {
  const threshold = LEVEL_ORDER[minLevel];
  const debugCategories = resolveDebugCategories();

  function emit(
    level: LogLevel,
    component: string,
    msg: string,
    fields?: Record<string, unknown>,
  ): void {
    if (LEVEL_ORDER[level] < threshold) return;
    // Debug-level category filtering: when VITE_DEBUG_CATEGORIES is set,
    // only emit debug messages whose component matches one of the listed categories.
    if (level === "debug" && debugCategories !== null) {
      if (!debugCategories.has(component.toLowerCase())) return;
    }

    const entry: LogEntry = {
      ts: new Date().toISOString(),
      level,
      component,
      msg,
      ...(fields && Object.keys(fields).length > 0 ? { fields } : {}),
    };

    const fn = LEVEL_FN[level];

    // logger.ts is the write-out primitive: every other consumer in src/
    // calls logger.warn/.info/etc rather than console directly (enforced by
    // the no-console ESLint rule in eslint.config.mjs). The three console
    // calls below are the implementation — disable the rule for them.
    if (import.meta.env.DEV) {
      // Readable format in development
      const tag = `[${component}]`;
      const lvl = level.toUpperCase().padEnd(5);
      if (fields && Object.keys(fields).length > 0) {
        // eslint-disable-next-line no-console
        console[fn](`${lvl} ${tag} ${msg}`, fields);
      } else {
        // eslint-disable-next-line no-console
        console[fn](`${lvl} ${tag} ${msg}`);
      }
    } else {
      // JSON output in production
      // eslint-disable-next-line no-console
      console[fn](JSON.stringify(entry));
    }
  }

  return {
    debug: (component: string, msg: string, fields?: Record<string, unknown>) =>
      emit("debug", component, msg, fields),
    info: (component: string, msg: string, fields?: Record<string, unknown>) =>
      emit("info", component, msg, fields),
    warn: (component: string, msg: string, fields?: Record<string, unknown>) =>
      emit("warn", component, msg, fields),
    error: (component: string, msg: string, fields?: Record<string, unknown>) =>
      emit("error", component, msg, fields),
  };
}

function resolveDefaultLevel(): LogLevel {
  const env = import.meta.env.VITE_LOG_LEVEL as string | undefined;
  if (env && env in LEVEL_ORDER) return env as LogLevel;
  return import.meta.env.DEV ? "debug" : "warn";
}

export const logger = createLogger(resolveDefaultLevel());
