import { raiseForStatus } from "./errors.js";

const INSPECT_CUSTOM = Symbol.for("nodejs.util.inspect.custom");
const EXPIRY_SKEW_MS = 30_000;

export interface AuthProvider {
  applyHeaders(headers: Headers, init: RequestInit): void | Promise<void>;
}

export interface SessionAuthOptions {
  baseUrl: string;
  email: string;
  password: string;
  fetch?: typeof globalThis.fetch;
}

type LoginResponse = {
  token?: string;
  expires_at?: string;
};

type RedactedJson = {
  type: string;
  redacted: true;
};

export class ApiKeyAuth implements AuthProvider {
  readonly #apiKey: string;
  readonly #header: string;

  public constructor(apiKey: string, options: { header?: string } = {}) {
    this.#apiKey = apiKey;
    this.#header = options.header ?? "X-API-Key";
  }

  public applyHeaders(headers: Headers, _init: RequestInit): void {
    headers.set(this.#header, this.#apiKey);
  }

  public toJSON(): RedactedJson {
    return { type: "apiKey", redacted: true };
  }

  public toString(): string {
    return "ApiKeyAuth { redacted: true }";
  }

  public [INSPECT_CUSTOM](): string {
    return this.toString();
  }
}

export class BearerTokenAuth implements AuthProvider {
  readonly #token: string;

  public constructor(token: string) {
    this.#token = token;
  }

  public applyHeaders(headers: Headers, _init: RequestInit): void {
    headers.set("Authorization", `Bearer ${this.#token}`);
  }

  public toJSON(): RedactedJson {
    return { type: "bearer", redacted: true };
  }

  public toString(): string {
    return "BearerTokenAuth { redacted: true }";
  }

  public [INSPECT_CUSTOM](): string {
    return this.toString();
  }
}

export class SessionAuth implements AuthProvider {
  readonly #baseUrl: string;
  readonly #email: string;
  readonly #password: string;
  readonly #fetch: typeof globalThis.fetch;
  #token: string | undefined;
  #expiresAtMs: number | undefined;
  #refreshPromise: Promise<string> | undefined;

  public constructor(options: SessionAuthOptions) {
    this.#baseUrl = options.baseUrl;
    this.#email = options.email;
    this.#password = options.password;
    this.#fetch = options.fetch ?? globalThis.fetch;
  }

  public async applyHeaders(headers: Headers, _init: RequestInit): Promise<void> {
    headers.set("Authorization", `Bearer ${await this.getToken()}`);
  }

  public async handleUnauthorizedResponse(response: Response): Promise<boolean> {
    if (response.status !== 401) {
      return false;
    }

    await this.refresh();
    return true;
  }

  public async refresh(): Promise<string> {
    return this.getToken(true);
  }

  public toJSON(): RedactedJson {
    return { type: "session", redacted: true };
  }

  public toString(): string {
    return "SessionAuth { redacted: true }";
  }

  public [INSPECT_CUSTOM](): string {
    return this.toString();
  }

  private async getToken(forceRefresh = false): Promise<string> {
    if (!forceRefresh && this.#token && !this.isExpiringSoon()) {
      return this.#token;
    }

    if (!this.#refreshPromise) {
      this.#refreshPromise = this.login(forceRefresh).finally(() => {
        this.#refreshPromise = undefined;
      });
    }

    return this.#refreshPromise;
  }

  private isExpiringSoon(): boolean {
    if (!this.#token) {
      return true;
    }
    if (this.#expiresAtMs === undefined) {
      return false;
    }
    return this.#expiresAtMs - Date.now() <= EXPIRY_SKEW_MS;
  }

  private async login(forceRefresh: boolean): Promise<string> {
    if (!forceRefresh && this.#token && !this.isExpiringSoon()) {
      return this.#token;
    }

    const response = await this.#fetch(new URL("/api/v1/auth/login", this.#baseUrl), {
      method: "POST",
      headers: {
        "content-type": "application/json",
      },
      body: JSON.stringify({
        username: this.#email,
        password: this.#password,
      }),
    });

    if (!response.ok) {
      await raiseForStatus(response);
    }

    const payload = (await response.json()) as LoginResponse;
    if (!payload.token || typeof payload.token !== "string") {
      throw new Error("Session login response did not include a token");
    }

    this.#token = payload.token;
    this.#expiresAtMs = decodeTokenExpiry(payload.token) ?? parseExpiryTimestamp(payload.expires_at);
    return payload.token;
  }
}

function decodeTokenExpiry(token: string): number | undefined {
  const segments = token.split(".");
  if (segments.length < 2) {
    return undefined;
  }

  const payloadSegment = segments[1];
  if (!payloadSegment) {
    return undefined;
  }

  try {
    const payload = JSON.parse(decodeBase64Url(payloadSegment)) as { exp?: unknown };
    return typeof payload.exp === "number" ? payload.exp * 1000 : undefined;
  } catch {
    return undefined;
  }
}

function parseExpiryTimestamp(value: string | undefined): number | undefined {
  if (!value) {
    return undefined;
  }

  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? undefined : parsed;
}

function decodeBase64Url(value: string): string {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");

  const atobFn = globalThis.atob;
  if (typeof atobFn === "function") {
    return atobFn(padded);
  }

  throw new Error("No base64 decoder available for JWT parsing");
}
