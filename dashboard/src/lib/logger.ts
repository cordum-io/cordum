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

function createLogger(minLevel: LogLevel) {
  const threshold = LEVEL_ORDER[minLevel];

  function emit(
    level: LogLevel,
    component: string,
    msg: string,
    fields?: Record<string, unknown>,
  ): void {
    if (LEVEL_ORDER[level] < threshold) return;

    const entry: LogEntry = {
      ts: new Date().toISOString(),
      level,
      component,
      msg,
      ...(fields && Object.keys(fields).length > 0 ? { fields } : {}),
    };

    const fn = LEVEL_FN[level];

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
