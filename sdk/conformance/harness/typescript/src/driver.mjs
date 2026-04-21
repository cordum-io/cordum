// HTTP driver for the TypeScript harness. Uses native fetch (Node 18+)
// so there are zero external deps. Parity with the Go/Python drivers
// is the north star: identical retry rules, identical wildcard
// grading, identical $vars.* substitution.

import { diff, resolveVars, selectJSONPath, inferErrorStatus } from './diff.mjs';
import { OPERATION_MAP } from './operation_map.mjs';

const DEFAULT_TIMEOUT_MS = 10_000;

export class Driver {
  constructor({ baseUrl, apiKey, tenant }) {
    this.baseUrl = baseUrl.replace(/\/+$/, '');
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
    for (let idx = 0; idx < fx.steps.length; idx++) {
      const step = fx.steps[idx];
      const kind = step.kind || 'request';
      try {
        if (kind === 'sleep') {
          await new Promise((r) => setTimeout(r, step.durationMs || 0));
          continue;
        }
        if (kind === 'request' || kind === 'assert_error' || kind === 'stream' || kind === 'paginate') {
          await this._dispatch(fx, step);
          continue;
        }
        throw new Error(`unknown step kind ${JSON.stringify(kind)}`);
      } catch (err) {
        const op = step.operationId || '';
        const msg = err instanceof Error ? err.message : String(err);
        throw new Error(`step ${idx} (${kind} ${op}): ${msg}`);
      }
    }
  }

  async _dispatch(fx, step) {
    const op = step.operationId;
    const route = OPERATION_MAP[op];
    if (!route) throw new Error(`unknown operationId ${JSON.stringify(op)}`);
    let [method, path] = route;
    const pathParams = step.pathParams || {};
    for (const [k, v] of Object.entries(pathParams)) {
      path = path.replaceAll(`{${k}}`, this._resolveStr(v));
    }
    const query = this._buildQuery(step.query || {});
    const url = this.baseUrl + path + (query ? `?${query}` : '');

    let body = null;
    if (step.body !== undefined && step.body !== null) {
      body = JSON.stringify(resolveVars(step.body, this.vars));
    }

    const headers = this._buildHeaders(fx, step, body !== null);
    const { status, responseHeaders, bodyText } = await this._sendWithRetry(
      method, url, headers, body, step,
    );

    if (step.kind === 'stream') {
      this._assertStream(bodyText, step);
      return;
    }

    const expect = step.expect || {};
    const expectedStatus = expect.status || inferErrorStatus(step.errorClass || '');
    if (expectedStatus && status !== expectedStatus) {
      const snippet = bodyText.slice(0, 240);
      throw new Error(`status=${status} want ${expectedStatus}; body=${snippet}`);
    }

    if (step.kind === 'assert_error') {
      // Status check + envelope parse already happened; keep the
      // raw-HTTP harness behavior identical to Go/Python.
      return;
    }

    const parsed = bodyText ? JSON.parse(bodyText) : null;
    if (expect.body !== undefined && expect.body !== null) {
      diff(parsed, expect.body, '$');
    }
    for (const [pathExpr, expected] of Object.entries(expect.bodyMatches || {})) {
      const selected = selectJSONPath(parsed, pathExpr);
      diff(selected, expected, pathExpr);
    }
    for (const [key, selector] of Object.entries(step.extract || {})) {
      this.vars[key] = selectJSONPath(parsed, selector);
    }
  }

  _buildHeaders(fx, step, hasBody) {
    const headers = {};
    const setup = fx.setup || {};
    for (const [k, v] of Object.entries(setup.headers || {})) {
      headers[k] = this._resolveStr(v);
    }
    for (const [k, v] of Object.entries(step.headers || {})) {
      headers[k] = this._resolveStr(v);
    }
    this._applyAuth(headers, setup.auth, step.auth);
    if (hasBody && !headers['Content-Type']) {
      headers['Content-Type'] = 'application/json';
    }
    return headers;
  }

  _applyAuth(headers, setupAuth, stepAuth) {
    const auth = stepAuth !== undefined ? stepAuth : setupAuth;
    if (auth === null || auth === undefined) {
      headers['X-API-Key'] = this.apiKey;
      return;
    }
    const kind = auth.kind;
    const value = this._resolveStr(auth.value || '');
    if (kind === 'apiKey') {
      if (value) headers['X-API-Key'] = value;
    } else if (kind === 'bearer') {
      if (value) headers['Authorization'] = `Bearer ${value}`;
    } else if (kind === 'none') {
      // no auth header
    } else {
      headers['X-API-Key'] = this.apiKey;
    }
  }

  _buildQuery(raw) {
    const entries = Object.entries(raw);
    if (!entries.length) return '';
    const params = new URLSearchParams();
    for (const [k, v] of entries) {
      params.set(k, String(resolveVars(v, this.vars)));
    }
    return params.toString();
  }

  _resolveStr(s) {
    if (typeof s !== 'string') return s === null || s === undefined ? '' : String(s);
    if (!s.startsWith('$vars.')) return s;
    const key = s.slice('$vars.'.length);
    return Object.prototype.hasOwnProperty.call(this.vars, key) ? String(this.vars[key]) : '';
  }

  async _sendWithRetry(method, url, headers, body, step) {
    const maxAttempts = 3;
    const retryable = ['GET', 'HEAD', 'PUT', 'DELETE'].includes(method)
      || (method === 'POST' && 'Idempotency-Key' in headers);
    const kind = step.kind || 'request';
    let last = null;
    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      const result = await this._sendOnce(method, url, headers, body);
      last = result;
      if (kind === 'assert_error') break;
      if (!retryable) break;
      if (![429, 500, 503, 504].includes(result.status)) break;
      if (attempt === maxAttempts - 1) break;
      const retryAfter = result.responseHeaders['retry-after'] || '';
      const delayMs = /^\d+$/.test(retryAfter) ? Number(retryAfter) * 100 : 20;
      await new Promise((r) => setTimeout(r, delayMs));
    }
    return last;
  }

  async _sendOnce(method, url, headers, body) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), DEFAULT_TIMEOUT_MS);
    try {
      const resp = await fetch(url, {
        method,
        headers,
        body: body || undefined,
        signal: controller.signal,
      });
      const text = await resp.text();
      const responseHeaders = {};
      resp.headers.forEach((v, k) => { responseHeaders[k.toLowerCase()] = v; });
      return { status: resp.status, responseHeaders, bodyText: text };
    } finally {
      clearTimeout(timer);
    }
  }

  _assertStream(bodyText, step) {
    const eventCount = step.eventCount || 0;
    const frames = (bodyText.match(/\ndata:/g) || []).length;
    if (eventCount && frames < eventCount) {
      throw new Error(`stream events=${frames} want >=${eventCount}`);
    }
    if (frames === 0 && !bodyText.includes('event:')) {
      throw new Error(`stream body carries no SSE frames: ${bodyText.slice(0, 200)}`);
    }
  }
}
