import {
  ApiKeyAuth,
  BearerTokenAuth,
  CordumClient,
  RetryExhaustedError,
} from "../../../../typescript/dist/index.mjs";

import { diff, resolveVars, selectJSONPath, inferErrorStatus } from "./diff.mjs";
import { OPERATION_MAP } from "./operation_map.mjs";

class AnonymousAuth {
  applyHeaders() {}
}

export class Driver {
  constructor({ baseUrl, apiKey, tenant }) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.apiKey = apiKey;
    this.tenant = tenant;
    this.vars = {};
    this.resetVars();
  }

  resetVars() {
    this.vars = { apiKey: this.apiKey, tenant: this.tenant };
  }

  async runFixture(fx) {
    this.resetVars();
    for (let idx = 0; idx < fx.steps.length; idx += 1) {
      const step = fx.steps[idx];
      const kind = step.kind || "request";
      try {
        if (kind === "sleep") {
          await new Promise((resolve) => setTimeout(resolve, step.durationMs || 0));
          continue;
        }
        if (["request", "assert_error", "stream", "paginate"].includes(kind)) {
          await this.#dispatch(fx, step);
          continue;
        }
        throw new Error(`unknown step kind ${JSON.stringify(kind)}`);
      } catch (err) {
        const op = step.operationId || "";
        const msg = err instanceof Error ? err.message : String(err);
        throw new Error(`step ${idx} (${kind} ${op}): ${msg}`);
      }
    }
  }

  async #dispatch(fx, step) {
    const route = OPERATION_MAP[step.operationId];
    if (!route) {
      throw new Error(`unknown operationId ${JSON.stringify(step.operationId)}`);
    }
    const [method, routePath] = route;
    let path = routePath;
    for (const [key, value] of Object.entries(step.pathParams || {})) {
      path = path.replaceAll(`{${key}}`, this.#resolveStr(value));
    }

    const headers = this.#buildHeaders(fx, step);
    const query = this.#buildQuery(step.query || {});
    const body = step.body !== undefined && step.body !== null ? resolveVars(step.body, this.vars) : undefined;

    const client = this.#buildClient(fx.setup?.auth, step.auth);
    try {
      if (step.kind === "stream") {
        await this.#assertStream(client, step);
        return;
      }
      if (step.kind === "paginate") {
        await this.#assertPaginate(client, method, path, headers, query, step);
        return;
      }

      let response;
      try {
        response = await client.requestRawDetailed(method, path, {
          headers,
          query,
          body,
        });
      } catch (err) {
        if (step.kind !== "assert_error") {
          throw err;
        }
        await this.#assertError(err, step);
        return;
      }

      if (step.kind === "assert_error") {
        const expected = step.expect?.errorClass || step.errorClass || "error";
        throw new Error(`expected ${expected} but request succeeded`);
      }

      const expect = step.expect || {};
      const expectedStatus = expect.status || 0;
      if (expectedStatus && response.status !== expectedStatus) {
        throw new Error(`status=${response.status} want ${expectedStatus}; body=${response.text.slice(0, 240)}`);
      }
      this.#assertResponse(response, step);
    } finally {
      client.close();
    }
  }

  #assertResponse(response, step) {
    const expect = step.expect || {};
    const parsed = response.data ?? null;

    if (expect.body !== undefined && expect.body !== null) {
      diff(parsed, expect.body, "$");
    }
    for (const [pathExpr, expected] of Object.entries(expect.bodyMatches || {})) {
      const selected = selectJSONPath(parsed, pathExpr);
      diff(selected, expected, pathExpr);
    }
    for (const [key, selector] of Object.entries(step.extract || {})) {
      this.vars[key] = selectJSONPath(parsed, selector);
    }
  }

  async #assertError(err, step) {
    const expect = step.expect || {};
    const expectedClass = expect.errorClass || step.errorClass || "";
    const actualClass = err instanceof RetryExhaustedError ? "RetryExhaustedError" : err?.constructor?.name || "Error";
    if (expectedClass && actualClass !== expectedClass) {
      throw new Error(`errorClass=${actualClass} want ${expectedClass}`);
    }

    const expectedStatus = expect.status || inferErrorStatus(expectedClass);
    const actualStatus = typeof err?.status === "number" ? err.status : undefined;
    if (expectedStatus && actualStatus !== expectedStatus) {
      throw new Error(`status=${actualStatus} want ${expectedStatus}`);
    }

    let payload = err?.payload;
    if (payload === undefined && err instanceof RetryExhaustedError && err.lastError instanceof Response) {
      payload = await err.lastError.clone().json().catch(async () => err.lastError.clone().text());
    }
    for (const [selector, expected] of Object.entries(expect.fields || {})) {
      const pathExpr = selector.startsWith("$") ? selector : `$.${selector}`;
      const selected = selectJSONPath(payload, pathExpr);
      diff(selected, expected, pathExpr);
    }
  }

  async #assertPaginate(client, method, path, headers, query, step) {
    const maxPages = step.maxPages || 10;
    let currentQuery = { ...query };
    let pageCount = 0;
    let totalItems = 0;

    while (pageCount < maxPages) {
      const response = await client.requestRawDetailed(method, path, {
        headers,
        query: currentQuery,
      });
      pageCount += 1;
      const body = response.data;
      if (!body || typeof body !== "object" || Array.isArray(body)) {
        throw new Error(`paginate response is ${typeof body}, want object`);
      }
      const items = body.items;
      if (!Array.isArray(items)) {
        throw new Error("paginate response missing items array");
      }
      totalItems += items.length;
      const cursor = body.nextCursor || body.cursor;
      const normalizedCursor = cursor || body.next_cursor;
      if (!normalizedCursor) {
        break;
      }
      currentQuery = { ...query, cursor: normalizedCursor };
    }

    this.#assertCount("pageCount", pageCount, step.expect?.pageCount);
    this.#assertCount("totalItems", totalItems, step.expect?.totalItems);
  }

  async #assertStream(client, step) {
    const maxEvents = step.maxEvents || (step.expect?.events?.length ?? 0);
    const events = [];
    for await (const event of client.streamEvents()) {
      events.push(event);
      if (maxEvents && events.length >= maxEvents) {
        break;
      }
    }

    if (!events.length) {
      throw new Error("stream body carries no SSE frames");
    }

    const expectedEvents = step.expect?.events || [];
    if (events.length < expectedEvents.length) {
      throw new Error(`stream events=${events.length} want >=${expectedEvents.length}`);
    }
    expectedEvents.forEach((expected, index) => {
      const actual = events[index];
      if (actual.event !== expected.type) {
        throw new Error(`stream event ${index} type=${actual.event} want ${expected.type}`);
      }
      diff(actual.data, expected.data, `$.events[${index}].data`);
    });
  }

  #buildClient(setupAuth, stepAuth) {
    const auth = stepAuth !== undefined ? stepAuth : setupAuth;
    if (auth === null || auth === undefined) {
      return new CordumClient({
        baseUrl: this.baseUrl,
        auth: this.apiKey,
        retryPolicy: { maxRetries: 2, jitter: false },
      });
    }

    const kind = auth.kind;
    const value = this.#resolveStr(auth.value || "");
    if (kind === "apiKey") {
      return new CordumClient({
        baseUrl: this.baseUrl,
        auth: new ApiKeyAuth(value),
        retryPolicy: { maxRetries: 2, jitter: false },
      });
    }
    if (kind === "bearer") {
      return new CordumClient({
        baseUrl: this.baseUrl,
        auth: new BearerTokenAuth(value),
        retryPolicy: { maxRetries: 2, jitter: false },
      });
    }
    if (kind === "none") {
      return new CordumClient({
        baseUrl: this.baseUrl,
        auth: new AnonymousAuth(),
        retryPolicy: { maxRetries: 2, jitter: false },
      });
    }
      return new CordumClient({
        baseUrl: this.baseUrl,
        auth: this.apiKey,
        retryPolicy: { maxRetries: 2, jitter: false },
      });
  }

  #buildHeaders(fx, step) {
    const headers = {};
    for (const [key, value] of Object.entries(fx.setup?.headers || {})) {
      headers[key] = this.#resolveStr(value);
    }
    for (const [key, value] of Object.entries(step.headers || {})) {
      headers[key] = this.#resolveStr(value);
    }
    return headers;
  }

  #buildQuery(raw) {
    const resolved = {};
    for (const [key, value] of Object.entries(raw)) {
      resolved[key] = resolveVars(value, this.vars);
    }
    return resolved;
  }

  #resolveStr(value) {
    if (typeof value !== "string") {
      return value === null || value === undefined ? "" : String(value);
    }
    if (!value.startsWith("$vars.")) {
      return value;
    }
    const key = value.slice("$vars.".length);
    return Object.prototype.hasOwnProperty.call(this.vars, key) ? String(this.vars[key]) : "";
  }

  #assertCount(name, actual, expected) {
    if (expected === null || expected === undefined) {
      return;
    }
    if (typeof expected === "number") {
      if (actual !== expected) {
        throw new Error(`${name}=${actual} want ${expected}`);
      }
      return;
    }
    if (typeof expected === "string" && expected.startsWith(">=")) {
      const want = Number(expected.slice(2).trim());
      if (actual < want) {
        throw new Error(`${name}=${actual} want >=${want}`);
      }
      return;
    }
    const want = Number(expected);
    if (actual !== want) {
      throw new Error(`${name}=${actual} want ${want}`);
    }
  }
}
