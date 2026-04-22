export interface CordumErrorOptions {
  status?: number | undefined;
  requestId?: string | undefined;
  payload?: unknown;
  code?: string | undefined;
  cause?: unknown;
}

interface ValidationErrorOptions extends CordumErrorOptions {
  fieldErrors?: Record<string, string[]> | undefined;
}

interface RateLimitErrorOptions extends CordumErrorOptions {
  retryAfterMs?: number | undefined;
}

interface RetryExhaustedErrorOptions extends CordumErrorOptions {
  lastError: unknown;
}

type ErrorEnvelope = {
  error?: string | {
    code?: string;
    message?: string;
    details?: {
      fields?: Record<string, string[] | string>;
    };
  };
  code?: string;
  message?: string;
  details?: {
    fields?: Record<string, string[] | string>;
  };
};

export class CordumError extends Error {
  public readonly status: number | undefined;
  public readonly requestId: string | undefined;
  public readonly payload: unknown;
  public readonly code: string | undefined;

  public constructor(message: string, options: CordumErrorOptions = {}) {
    super(message, options.cause !== undefined ? { cause: options.cause } : undefined);
    this.name = new.target.name;
    this.status = options.status;
    this.requestId = options.requestId;
    this.payload = options.payload;
    this.code = options.code;
    Object.setPrototypeOf(this, new.target.prototype);
    Error.captureStackTrace?.(this, new.target);
  }

  public override toString(): string {
    const parts = [this.name, this.message];
    if (this.status !== undefined) {
      parts.push(`status=${this.status}`);
    }
    if (this.requestId) {
      parts.push(`requestId=${this.requestId}`);
    }
    if (this.code) {
      parts.push(`code=${this.code}`);
    }
    return parts.join(" ");
  }

  public [Symbol.toPrimitive](hint: string): string | number | null {
    if (hint === "number") {
      return this.status ?? null;
    }
    return this.toString();
  }
}

export class AuthenticationError extends CordumError {}
export class AuthorizationError extends CordumError {}
export class NotFoundError extends CordumError {}
export class ConflictError extends CordumError {}
export class ServerError extends CordumError {}
export class NetworkError extends CordumError {}
export class TimeoutError extends CordumError {}

export class ValidationError extends CordumError {
  public readonly fieldErrors: Record<string, string[]>;

  public constructor(message: string, options: ValidationErrorOptions = {}) {
    super(message, options);
    this.fieldErrors = options.fieldErrors ?? {};
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class RateLimitError extends CordumError {
  public readonly retryAfterMs: number | undefined;

  public constructor(message: string, options: RateLimitErrorOptions = {}) {
    super(message, options);
    this.retryAfterMs = options.retryAfterMs;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

export class RetryExhaustedError extends CordumError {
  public readonly lastError: unknown;

  public constructor(message: string, options: RetryExhaustedErrorOptions) {
    super(message, { ...options, cause: options.lastError });
    this.lastError = options.lastError;
    Object.setPrototypeOf(this, new.target.prototype);
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function normalizeFieldErrors(fields: unknown): Record<string, string[]> {
  if (!isRecord(fields)) {
    return {};
  }

  return Object.fromEntries(
    Object.entries(fields).map(([field, value]) => {
      if (Array.isArray(value)) {
        return [field, value.map((entry) => String(entry))];
      }
      return [field, [String(value)]];
    }),
  );
}

function extractEnvelope(payload: unknown): ErrorEnvelope | undefined {
  return isRecord(payload) ? (payload as ErrorEnvelope) : undefined;
}

function extractMessage(payload: unknown, fallback: string): string {
  const envelope = extractEnvelope(payload);
  if (!envelope) {
    return typeof payload === "string" && payload.trim() ? payload : fallback;
  }

  if (typeof envelope.error === "object" && envelope.error?.message) {
    return envelope.error.message;
  }
  if (typeof envelope.message === "string" && envelope.message.trim()) {
    return envelope.message;
  }
  if (typeof envelope.error === "string" && envelope.error.trim()) {
    return envelope.error;
  }

  return fallback;
}

function extractCode(payload: unknown): string | undefined {
  const envelope = extractEnvelope(payload);
  if (!envelope) {
    return undefined;
  }
  if (typeof envelope.error === "object" && envelope.error?.code) {
    return envelope.error.code;
  }
  return typeof envelope.code === "string" ? envelope.code : undefined;
}

function extractFieldErrors(payload: unknown): Record<string, string[]> {
  const envelope = extractEnvelope(payload);
  if (!envelope) {
    return {};
  }
  if (typeof envelope.error === "object") {
    return normalizeFieldErrors(envelope.error?.details?.fields);
  }
  return normalizeFieldErrors(envelope.details?.fields);
}

async function parsePayload(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) {
    return undefined;
  }

  const contentType = response.headers.get("content-type")?.toLowerCase() ?? "";
  if (!(contentType.includes("application/json") || contentType.includes("+json"))) {
    try {
      return JSON.parse(text);
    } catch {
      return text;
    }
  }

  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

export function parseRetryAfterHeader(value: string | null | undefined, now = Date.now()): number | undefined {
  if (!value) {
    return undefined;
  }

  const trimmed = value.trim();
  if (!trimmed) {
    return undefined;
  }

  const seconds = Number(trimmed);
  if (Number.isFinite(seconds)) {
    return Math.max(0, Math.round(seconds * 1000));
  }

  const dateMs = Date.parse(trimmed);
  if (Number.isNaN(dateMs)) {
    return undefined;
  }

  return Math.max(0, dateMs - now);
}

export function isCordumError(error: unknown): error is CordumError {
  return error instanceof CordumError;
}

export async function raiseForStatus(response: Response, requestId?: string): Promise<never> {
  const payload = await parsePayload(response);
  const resolvedRequestId = requestId ?? response.headers.get("X-Request-Id") ?? undefined;
  const status = response.status;
  const message = extractMessage(payload, response.statusText || `HTTP ${status}`);
  const code = extractCode(payload);
  const baseOptions = {
    status,
    requestId: resolvedRequestId,
    payload,
    code,
  } satisfies CordumErrorOptions;

  if (status === 400 || status === 422) {
    throw new ValidationError(message, {
      ...baseOptions,
      fieldErrors: extractFieldErrors(payload),
    });
  }
  if (status === 401) {
    throw new AuthenticationError(message, baseOptions);
  }
  if (status === 403) {
    throw new AuthorizationError(message, baseOptions);
  }
  if (status === 404) {
    throw new NotFoundError(message, baseOptions);
  }
  if (status === 409) {
    throw new ConflictError(message, baseOptions);
  }
  if (status === 429) {
    throw new RateLimitError(message, {
      ...baseOptions,
      retryAfterMs: parseRetryAfterHeader(response.headers.get("Retry-After")),
    });
  }
  if (status >= 500) {
    throw new ServerError(message, baseOptions);
  }

  throw new CordumError(message, baseOptions);
}
