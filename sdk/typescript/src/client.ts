import { ApiKeyAuth, type AuthProvider } from "./auth.js";
import { createGeneratedClient, type CordumFetchClient, type paths } from "./_generated/fetch.js";
import { withAuth, withTenantHeader, withTimeout } from "./_middleware.js";
import { raiseForStatus } from "./errors.js";
import { createRetryFetch, type RetryPolicy } from "./retry.js";
import { getNextCursor, paginate, type PaginatedEnvelope } from "./pagination.js";
import { streamEvents, type StreamEventsOptions } from "./streaming.js";
import { version } from "./version.js";

type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
type LowerMethod<T extends HttpMethod> = Lowercase<T>;
type Operation<Path extends keyof paths, Method extends HttpMethod> = NonNullable<paths[Path][LowerMethod<Method>]>;
type SuccessResponse<Responses> =
  Responses extends { 200: infer Response200 } ? Response200 :
  Responses extends { 201: infer Response201 } ? Response201 :
  Responses extends { 202: infer Response202 } ? Response202 :
  Responses extends { 204: infer Response204 } ? Response204 :
  never;
type ResponseContent<Response> =
  Response extends { content: { "application/json": infer Json } } ? Json :
  Response extends { content: { "text/event-stream": infer Stream } } ? Stream :
  void;
type OperationResponse<Op> = Op extends { responses: infer Responses } ? ResponseContent<SuccessResponse<Responses>> : never;
type OperationBody<Op> = Op extends { requestBody: { content: { "application/json": infer Body } } } ? Body : never;
type OperationQuery<Op> = Op extends { parameters: { query: infer Query } } ? Query : never;
type OperationPath<Op> = Op extends { parameters: { path: infer PathParams } } ? PathParams : never;

type GetResponse<Path extends keyof paths> = OperationResponse<Operation<Path, "GET">>;
type GetQuery<Path extends keyof paths> = OperationQuery<Operation<Path, "GET">>;
type PostBody<Path extends keyof paths> = OperationBody<Operation<Path, "POST">>;
type PutBody<Path extends keyof paths> = OperationBody<Operation<Path, "PUT">>;
type DeletePath<Path extends keyof paths> = OperationPath<Operation<Path, "DELETE">>;
type GetPath<Path extends keyof paths> = OperationPath<Operation<Path, "GET">>;
type PostPath<Path extends keyof paths> = OperationPath<Operation<Path, "POST">>;
type PutPath<Path extends keyof paths> = OperationPath<Operation<Path, "PUT">>;
type PaginatedItem<TPage> = TPage extends { items?: infer Items }
  ? Items extends Array<infer Item> | ReadonlyArray<infer Item>
    ? Item
    : never
  : never;

export interface CordumClientOptions {
  baseUrl: string;
  auth: AuthProvider | string;
  timeoutMs?: number;
  retryPolicy?: RetryPolicy;
  tenantId?: string;
  userAgent?: string;
  fetch?: typeof globalThis.fetch;
  logger?: Pick<Console, "debug" | "warn" | "error">;
}

export interface RequestDetailedOptions {
  query?: Record<string, unknown>;
  headers?: Record<string, string>;
  body?: unknown;
  signal?: AbortSignal;
}

export interface RequestDetailedResponse<TData = unknown> {
  status: number;
  headers: Headers;
  data: TData | undefined;
  text: string;
}

type SessionAuthLike = AuthProvider & {
  handleUnauthorizedResponse?(response: Response): Promise<boolean>;
};

export class CordumClient {
  readonly #baseUrl: string;
  readonly #auth: SessionAuthLike;
  readonly #raw: CordumFetchClient;
  readonly #rootAbortController = new AbortController();
  readonly #requestFetch: typeof globalThis.fetch;
  readonly #streamFetch: typeof globalThis.fetch;
  readonly #logger: Pick<Console, "debug" | "warn" | "error"> | undefined;
  #closed = false;
  #jobs?: ReturnType<CordumClient["createJobsNamespace"]>;
  #workflows?: ReturnType<CordumClient["createWorkflowsNamespace"]>;
  #policies?: ReturnType<CordumClient["createPoliciesNamespace"]>;
  #workers?: ReturnType<CordumClient["createWorkersNamespace"]>;
  #agents?: ReturnType<CordumClient["createAgentsNamespace"]>;
  #mcp?: ReturnType<CordumClient["createMcpNamespace"]>;
  #telemetry?: ReturnType<CordumClient["createTelemetryNamespace"]>;
  #authNamespace?: ReturnType<CordumClient["createAuthNamespace"]>;
  #identities?: ReturnType<CordumClient["createIdentitiesNamespace"]>;
  #credentials?: ReturnType<CordumClient["createCredentialsNamespace"]>;
  #velocity?: ReturnType<CordumClient["createVelocityNamespace"]>;
  #legalHold?: ReturnType<CordumClient["createLegalHoldNamespace"]>;
  #rbac?: ReturnType<CordumClient["createRbacNamespace"]>;

  public constructor(options: CordumClientOptions) {
    this.#baseUrl = options.baseUrl;
    this.#logger = options.logger;
    this.#auth = typeof options.auth === "string" ? new ApiKeyAuth(options.auth) : options.auth;

    const userFetch = options.fetch ?? globalThis.fetch;
    const userAgent = [options.userAgent, `@cordum/sdk/${version}`].filter(Boolean).join(" ");
    const tenantFetch = withTenantHeader(userFetch, { tenantId: options.tenantId, userAgent });
    this.#streamFetch = withAuth(tenantFetch, this.#auth);
    const timeoutFetch = withTimeout(tenantFetch, {
      timeoutMs: options.timeoutMs ?? 30_000,
      rootSignal: this.#rootAbortController.signal,
      logger: options.logger,
    });
    const authFetch = withAuth(timeoutFetch, this.#auth);
    const composedFetch = createRetryFetch(authFetch, options.retryPolicy);
    this.#requestFetch = composedFetch;
    this.#raw = createGeneratedClient({
      baseUrl: options.baseUrl,
      fetch: composedFetch,
    });
  }

  public get jobs() {
    return (this.#jobs ??= this.createJobsNamespace());
  }

  public get workflows() {
    return (this.#workflows ??= this.createWorkflowsNamespace());
  }

  public get policies() {
    return (this.#policies ??= this.createPoliciesNamespace());
  }

  public get workers() {
    return (this.#workers ??= this.createWorkersNamespace());
  }

  public get agents() {
    return (this.#agents ??= this.createAgentsNamespace());
  }

  public get mcp() {
    return (this.#mcp ??= this.createMcpNamespace());
  }

  public get telemetry() {
    return (this.#telemetry ??= this.createTelemetryNamespace());
  }

  public get auth() {
    return (this.#authNamespace ??= this.createAuthNamespace());
  }

  public get identities() {
    return (this.#identities ??= this.createIdentitiesNamespace());
  }

  public get credentials() {
    return (this.#credentials ??= this.createCredentialsNamespace());
  }

  public get velocity() {
    return (this.#velocity ??= this.createVelocityNamespace());
  }

  public get legalHold() {
    return (this.#legalHold ??= this.createLegalHoldNamespace());
  }

  public get rbac() {
    return (this.#rbac ??= this.createRbacNamespace());
  }

  public close(): void {
    if (this.#closed) {
      return;
    }
    this.#closed = true;
    this.#rootAbortController.abort(new DOMException("Cordum client closed", "AbortError"));
  }

  public streamEvents(options: Omit<StreamEventsOptions, "baseUrl" | "fetch" | "rootSignal"> = {}) {
    return streamEvents({
      baseUrl: this.#baseUrl,
      fetch: this.#streamFetch,
      rootSignal: this.#rootAbortController.signal,
      ...options,
    });
  }

  public async requestRawDetailed<TData = unknown>(
    method: HttpMethod,
    path: string,
    options: RequestDetailedOptions = {},
    allowAuthRefresh = true,
  ): Promise<RequestDetailedResponse<TData>> {
    if (this.#closed) {
      throw new DOMException("Cordum client closed", "AbortError");
    }

    const url = new URL(path, this.#baseUrl);
    for (const [key, value] of Object.entries(options.query ?? {})) {
      if (value === undefined || value === null) {
        continue;
      }
      url.searchParams.set(key, String(value));
    }

    const headers = new Headers(options.headers ?? {});
    let body: string | undefined;
    if (options.body !== undefined) {
      headers.set("content-type", headers.get("content-type") ?? "application/json");
      body = JSON.stringify(options.body);
    }

    const init: RequestInit = {
      method,
      headers,
      ...(body !== undefined ? { body } : {}),
      ...(options.signal !== undefined ? { signal: options.signal } : {}),
    };

    const response = await this.#requestFetch(url, init);

    if (response.status === 401 && allowAuthRefresh && this.supportsUnauthorizedRefresh()) {
      const refreshed = await this.#auth.handleUnauthorizedResponse?.(response);
      if (refreshed) {
        return this.requestRawDetailed<TData>(method, path, options, false);
      }
    }

    if (!response.ok) {
      await raiseForStatus(response);
    }

    const parsed = await parseResponseBody<TData>(response);
    return {
      status: response.status,
      headers: response.headers,
      data: parsed.data,
      text: parsed.text,
    };
  }

  public async requestRaw<TData = unknown>(
    method: HttpMethod,
    path: string,
    options: RequestDetailedOptions = {},
  ): Promise<TData | undefined> {
    const response = await this.requestRawDetailed<TData>(method, path, options);
    return response.data;
  }

  private createJobsNamespace() {
    return {
      list: (query?: GetQuery<"/api/v1/jobs">) =>
        this.request<"/api/v1/jobs", "GET">("GET", "/api/v1/jobs", query ? { params: { query } } : undefined),
      paginate: (query?: GetQuery<"/api/v1/jobs">) =>
        paginate<PaginatedItem<GetResponse<"/api/v1/jobs">>, GetResponse<"/api/v1/jobs"> & PaginatedEnvelope<PaginatedItem<GetResponse<"/api/v1/jobs">>>>(
          (signal) =>
            this.request<"/api/v1/jobs", "GET">("GET", "/api/v1/jobs", {
              params: { query },
              signal,
            }) as Promise<GetResponse<"/api/v1/jobs"> & PaginatedEnvelope<PaginatedItem<GetResponse<"/api/v1/jobs">>>>,
          getNextCursor,
          (cursor, signal) =>
            this.request<"/api/v1/jobs", "GET">("GET", "/api/v1/jobs", {
              params: { query: { ...(query ?? {}), cursor } },
              signal,
            }) as Promise<GetResponse<"/api/v1/jobs"> & PaginatedEnvelope<PaginatedItem<GetResponse<"/api/v1/jobs">>>>,
        ),
      get: (id: GetPath<"/api/v1/jobs/{id}">["id"]) =>
        this.request<"/api/v1/jobs/{id}", "GET">("GET", "/api/v1/jobs/{id}", { params: { path: { id } } }),
      create: (
        body: PostBody<"/api/v1/jobs">,
        options: { idempotencyKey?: string } = {},
      ) =>
        this.request<"/api/v1/jobs", "POST">("POST", "/api/v1/jobs", {
          body,
          headers: options.idempotencyKey ? { "Idempotency-Key": options.idempotencyKey } : undefined,
        }),
      decisions: (id: GetPath<"/api/v1/jobs/{id}/decisions">["id"]) =>
        this.request<"/api/v1/jobs/{id}/decisions", "GET">("GET", "/api/v1/jobs/{id}/decisions", {
          params: { path: { id } },
        }),
      cancel: (id: PostPath<"/api/v1/jobs/{id}/cancel">["id"]) =>
        this.request<"/api/v1/jobs/{id}/cancel", "POST">("POST", "/api/v1/jobs/{id}/cancel", {
          params: { path: { id } },
        }),
      remediate: (id: PostPath<"/api/v1/jobs/{id}/remediate">["id"], body: PostBody<"/api/v1/jobs/{id}/remediate">) =>
        this.request<"/api/v1/jobs/{id}/remediate", "POST">("POST", "/api/v1/jobs/{id}/remediate", {
          body,
          params: { path: { id } },
        }),
      stream: (id: GetPath<"/api/v1/jobs/{id}/stream">["id"]) =>
        this.request<"/api/v1/jobs/{id}/stream", "GET">("GET", "/api/v1/jobs/{id}/stream", {
          params: { path: { id } },
        }),
    };
  }

  private createWorkflowsNamespace() {
    return {
      list: () => this.request<"/api/v1/workflows", "GET">("GET", "/api/v1/workflows"),
      paginate: () =>
        paginate<PaginatedItem<{ items: GetResponse<"/api/v1/workflows"> }>, { items: GetResponse<"/api/v1/workflows">; nextCursor?: string | null }>(
          async () => ({ items: await this.request<"/api/v1/workflows", "GET">("GET", "/api/v1/workflows") }),
          getNextCursor,
          async () => ({ items: [] as GetResponse<"/api/v1/workflows">, nextCursor: null }),
        ),
      get: (id: GetPath<"/api/v1/workflows/{id}">["id"]) =>
        this.request<"/api/v1/workflows/{id}", "GET">("GET", "/api/v1/workflows/{id}", {
          params: { path: { id } },
        }),
      create: (body: PostBody<"/api/v1/workflows">) =>
        this.request<"/api/v1/workflows", "POST">("POST", "/api/v1/workflows", { body }),
      listRuns: (id: GetPath<"/api/v1/workflows/{id}/runs">["id"]) =>
        this.request<"/api/v1/workflows/{id}/runs", "GET">("GET", "/api/v1/workflows/{id}/runs", {
          params: { path: { id } },
        }),
      dryRun: (id: PostPath<"/api/v1/workflows/{id}/dry-run">["id"], body: PostBody<"/api/v1/workflows/{id}/dry-run">) =>
        this.request<"/api/v1/workflows/{id}/dry-run", "POST">("POST", "/api/v1/workflows/{id}/dry-run", {
          body,
          params: { path: { id } },
        }),
    };
  }

  private createPoliciesNamespace() {
    return {
      evaluate: (body: PostBody<"/api/v1/policy/evaluate">) =>
        this.request<"/api/v1/policy/evaluate", "POST">("POST", "/api/v1/policy/evaluate", { body }),
      simulate: (body: PostBody<"/api/v1/policy/simulate">) =>
        this.request<"/api/v1/policy/simulate", "POST">("POST", "/api/v1/policy/simulate", { body }),
      explain: (body: PostBody<"/api/v1/policy/explain">) =>
        this.request<"/api/v1/policy/explain", "POST">("POST", "/api/v1/policy/explain", { body }),
      listBundles: () => this.request<"/api/v1/policy/bundles", "GET">("GET", "/api/v1/policy/bundles"),
      getBundle: (id: GetPath<"/api/v1/policy/bundles/{id}">["id"]) =>
        this.request<"/api/v1/policy/bundles/{id}", "GET">("GET", "/api/v1/policy/bundles/{id}", {
          params: { path: { id } },
        }),
      listRules: () => this.request<"/api/v1/policy/rules", "GET">("GET", "/api/v1/policy/rules"),
    };
  }

  private createWorkersNamespace() {
    return {
      list: () => this.request<"/api/v1/workers", "GET">("GET", "/api/v1/workers"),
      get: (id: GetPath<"/api/v1/workers/{id}">["id"]) =>
        this.request<"/api/v1/workers/{id}", "GET">("GET", "/api/v1/workers/{id}", {
          params: { path: { id } },
        }),
      listJobs: (id: GetPath<"/api/v1/workers/{id}/jobs">["id"]) =>
        this.request<"/api/v1/workers/{id}/jobs", "GET">("GET", "/api/v1/workers/{id}/jobs", {
          params: { path: { id } },
        }),
    };
  }

  private createAgentsNamespace() {
    return {
      list: () => this.workers.list(),
      get: (id: GetPath<"/api/v1/workers/{id}">["id"]) => this.workers.get(id),
      listJobs: (id: GetPath<"/api/v1/workers/{id}/jobs">["id"]) => this.workers.listJobs(id),
    };
  }

  private createMcpNamespace() {
    return {
      sse: () => this.request<"/mcp/sse", "GET">("GET", "/mcp/sse"),
      message: (body: PostBody<"/mcp/message">) =>
        this.request<"/mcp/message", "POST">("POST", "/mcp/message", { body }),
      status: () => this.request<"/mcp/status", "GET">("GET", "/mcp/status"),
    };
  }

  private createTelemetryNamespace() {
    return {
      status: () => this.request<"/api/v1/telemetry/status", "GET">("GET", "/api/v1/telemetry/status"),
      inspect: (body: PostBody<"/api/v1/telemetry/inspect">) =>
        this.request<"/api/v1/telemetry/inspect", "POST">("POST", "/api/v1/telemetry/inspect", { body }),
      export: (body: PostBody<"/api/v1/telemetry/export">) =>
        this.request<"/api/v1/telemetry/export", "POST">("POST", "/api/v1/telemetry/export", { body }),
      usage: () => this.request<"/api/v1/telemetry/usage", "GET">("GET", "/api/v1/telemetry/usage"),
      consent: (body: PostBody<"/api/v1/telemetry/consent">) =>
        this.request<"/api/v1/telemetry/consent", "POST">("POST", "/api/v1/telemetry/consent", { body }),
    };
  }

  private createAuthNamespace() {
    return {
      config: () => this.request<"/api/v1/auth/config", "GET">("GET", "/api/v1/auth/config"),
      login: (body: PostBody<"/api/v1/auth/login">) =>
        this.request<"/api/v1/auth/login", "POST">("POST", "/api/v1/auth/login", { body }),
      session: () => this.request<"/api/v1/auth/session", "GET">("GET", "/api/v1/auth/session"),
      logout: () => this.request<"/api/v1/auth/logout", "POST">("POST", "/api/v1/auth/logout"),
      changePassword: (body: PostBody<"/api/v1/auth/password">) =>
        this.request<"/api/v1/auth/password", "POST">("POST", "/api/v1/auth/password", { body }),
      listKeys: () => this.request<"/api/v1/auth/keys", "GET">("GET", "/api/v1/auth/keys"),
      createKey: (body: PostBody<"/api/v1/auth/keys">) =>
        this.request<"/api/v1/auth/keys", "POST">("POST", "/api/v1/auth/keys", { body }),
      deleteKey: (id: DeletePath<"/api/v1/auth/keys/{id}">["id"]) =>
        this.request<"/api/v1/auth/keys/{id}", "DELETE">("DELETE", "/api/v1/auth/keys/{id}", {
          params: { path: { id } },
        }),
    };
  }

  private createIdentitiesNamespace() {
    return {
      list: () => this.request<"/api/v1/users", "GET">("GET", "/api/v1/users"),
      create: (body: PostBody<"/api/v1/users">) => this.request<"/api/v1/users", "POST">("POST", "/api/v1/users", { body }),
      update: (id: PutPath<"/api/v1/users/{id}">["id"], body: PutBody<"/api/v1/users/{id}">) =>
        this.request<"/api/v1/users/{id}", "PUT">("PUT", "/api/v1/users/{id}", {
          body,
          params: { path: { id } },
        }),
      delete: (id: DeletePath<"/api/v1/users/{id}">["id"]) =>
        this.request<"/api/v1/users/{id}", "DELETE">("DELETE", "/api/v1/users/{id}", {
          params: { path: { id } },
        }),
      resetPassword: (id: PostPath<"/api/v1/users/{id}/password">["id"], body: PostBody<"/api/v1/users/{id}/password">) =>
        this.request<"/api/v1/users/{id}/password", "POST">("POST", "/api/v1/users/{id}/password", {
          body,
          params: { path: { id } },
        }),
    };
  }

  private createCredentialsNamespace() {
    return {
      listWorkers: () => this.request<"/api/v1/workers/credentials", "GET">("GET", "/api/v1/workers/credentials"),
      createWorker: (body: PostBody<"/api/v1/workers/credentials">) =>
        this.request<"/api/v1/workers/credentials", "POST">("POST", "/api/v1/workers/credentials", { body }),
      deleteWorker: (worker_id: DeletePath<"/api/v1/workers/credentials/{worker_id}">["worker_id"]) =>
        this.request<"/api/v1/workers/credentials/{worker_id}", "DELETE">("DELETE", "/api/v1/workers/credentials/{worker_id}", {
          params: { path: { worker_id } },
        }),
    };
  }

  private createVelocityNamespace() {
    return {
      list: () => this.request<"/api/v1/policy/velocity-rules", "GET">("GET", "/api/v1/policy/velocity-rules"),
      create: (body: PostBody<"/api/v1/policy/velocity-rules">) =>
        this.request<"/api/v1/policy/velocity-rules", "POST">("POST", "/api/v1/policy/velocity-rules", { body }),
      stats: () => this.request<"/api/v1/policy/velocity-rules/stats", "GET">("GET", "/api/v1/policy/velocity-rules/stats"),
      update: (id: PutPath<"/api/v1/policy/velocity-rules/{id}">["id"], body: PutBody<"/api/v1/policy/velocity-rules/{id}">) =>
        this.request<"/api/v1/policy/velocity-rules/{id}", "PUT">("PUT", "/api/v1/policy/velocity-rules/{id}", {
          body,
          params: { path: { id } },
        }),
      delete: (id: DeletePath<"/api/v1/policy/velocity-rules/{id}">["id"]) =>
        this.request<"/api/v1/policy/velocity-rules/{id}", "DELETE">("DELETE", "/api/v1/policy/velocity-rules/{id}", {
          params: { path: { id } },
        }),
    };
  }

  private createLegalHoldNamespace() {
    return {
      create: (body: PostBody<"/api/v1/audit/legal-hold">) =>
        this.request<"/api/v1/audit/legal-hold", "POST">("POST", "/api/v1/audit/legal-hold", { body }),
      list: () => this.request<"/api/v1/audit/legal-holds", "GET">("GET", "/api/v1/audit/legal-holds"),
      release: (id: DeletePath<"/api/v1/audit/legal-hold/{id}">["id"]) =>
        this.request<"/api/v1/audit/legal-hold/{id}", "DELETE">("DELETE", "/api/v1/audit/legal-hold/{id}", {
          params: { path: { id } },
        }),
    };
  }

  private createRbacNamespace() {
    return {
      list: () => this.request<"/api/v1/auth/roles", "GET">("GET", "/api/v1/auth/roles"),
      get: (name: GetPath<"/api/v1/auth/roles/{name}">["name"]) =>
        this.request<"/api/v1/auth/roles/{name}", "GET">("GET", "/api/v1/auth/roles/{name}", {
          params: { path: { name } },
        }),
      put: (name: PutPath<"/api/v1/auth/roles/{name}">["name"], body: PutBody<"/api/v1/auth/roles/{name}">) =>
        this.request<"/api/v1/auth/roles/{name}", "PUT">("PUT", "/api/v1/auth/roles/{name}", {
          body,
          params: { path: { name } },
        }),
      delete: (name: DeletePath<"/api/v1/auth/roles/{name}">["name"]) =>
        this.request<"/api/v1/auth/roles/{name}", "DELETE">("DELETE", "/api/v1/auth/roles/{name}", {
          params: { path: { name } },
        }),
    };
  }

  private async request<Path extends keyof paths, Method extends HttpMethod>(
    method: Method,
    path: Path,
    options?: unknown,
    allowAuthRefresh = true,
  ): Promise<OperationResponse<Operation<Path, Method>>> {
    if (this.#closed) {
      throw new DOMException("Cordum client closed", "AbortError");
    }

    const result = await (this.#raw[method] as (path: Path, options?: unknown) => Promise<{
      data?: OperationResponse<Operation<Path, Method>>;
      error?: unknown;
      response: Response;
    }>)(path, options);

    if (result.response.status === 401 && allowAuthRefresh && this.supportsUnauthorizedRefresh()) {
      const refreshed = await this.#auth.handleUnauthorizedResponse?.(result.response);
      if (refreshed) {
        return this.request(method, path, options, false);
      }
    }

    if (result.error !== undefined) {
      await raiseForStatus(createErrorResponse(result.response, result.error), result.response.headers.get("X-Request-Id") ?? undefined);
    }

    return result.data as OperationResponse<Operation<Path, Method>>;
  }

  private supportsUnauthorizedRefresh(): boolean {
    return typeof this.#auth.handleUnauthorizedResponse === "function";
  }
}

function createErrorResponse(response: Response, payload: unknown): Response {
  const headers = new Headers(response.headers);
  let body: BodyInit | null = null;

  if (payload !== undefined) {
    if (typeof payload === "string") {
      body = payload;
      headers.set("content-type", headers.get("content-type") ?? "text/plain;charset=utf-8");
    } else {
      body = JSON.stringify(payload);
      headers.set("content-type", headers.get("content-type") ?? "application/json");
    }
  }

  return new Response(body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}

async function parseResponseBody<TData>(response: Response): Promise<{ data: TData | undefined; text: string }> {
  if (response.status === 204) {
    return { data: undefined, text: "" };
  }

  const text = await response.text();
  if (!text) {
    return { data: undefined, text };
  }

  const contentType = response.headers.get("content-type")?.toLowerCase() ?? "";
  if (contentType.includes("application/json")) {
    return {
      data: JSON.parse(text) as TData,
      text,
    };
  }

  return {
    data: text as TData,
    text,
  };
}
