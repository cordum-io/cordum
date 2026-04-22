// Shared grading engine for the TypeScript harness. Verdicts must
// match the Go and Python engines byte-for-byte; step-9 parity runner
// asserts this.
//
// Wildcards:
//   $any$         any value passes
//   $timestamp$   string parsable as RFC3339 / ISO-8601
//   $uuid$        string matching UUID v4 shape
//   $int$         integer value (JSON number round-tripping to int)
//   $request_id$  opaque request id (behaves like $any$ for now)

const WILDCARDS = new Set([
  '$any$', '$timestamp$', '$uuid$', '$int$', '$request_id$',
]);

const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

export class DiffError extends Error {
  constructor(path, message) {
    super(`${path}: ${message}`);
    this.name = 'DiffError';
    this.path = path;
  }
}

export function isWildcard(v) {
  return typeof v === 'string' && WILDCARDS.has(v);
}

export function diff(actual, expected, path = '$') {
  if (isWildcard(expected)) {
    checkWildcard(actual, expected, path);
    return;
  }
  if (expected === null) {
    if (actual !== null) {
      throw new DiffError(path, `want null, got ${JSON.stringify(actual)}`);
    }
    return;
  }
  if (Array.isArray(expected)) {
    if (!Array.isArray(actual)) {
      throw new DiffError(path, `want array, got ${typeof actual}`);
    }
    if (actual.length !== expected.length) {
      throw new DiffError(path, `length mismatch (want ${expected.length}, got ${actual.length})`);
    }
    for (let i = 0; i < expected.length; i++) {
      diff(actual[i], expected[i], `${path}[${i}]`);
    }
    return;
  }
  if (typeof expected === 'object') {
    if (actual === null || typeof actual !== 'object' || Array.isArray(actual)) {
      throw new DiffError(path, `want object, got ${Array.isArray(actual) ? 'array' : typeof actual}`);
    }
    const keys = Object.keys(expected).sort();
    for (const k of keys) {
      if (!(k in actual)) {
        throw new DiffError(`${path}.${k}`, 'expected key missing from response');
      }
      diff(actual[k], expected[k], `${path}.${k}`);
    }
    return;
  }
  if (actual !== expected) {
    throw new DiffError(path, `want ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}

function checkWildcard(actual, token, path) {
  if (token === '$any$' || token === '$request_id$') return;
  if (token === '$timestamp$') {
    if (typeof actual !== 'string') {
      throw new DiffError(path, `$timestamp$ expects string, got ${typeof actual}`);
    }
    // Normalize trailing Z to +00:00 so Date() parses consistently.
    const normalized = actual.endsWith('Z') ? actual.slice(0, -1) + '+00:00' : actual;
    const parsed = new Date(normalized);
    if (Number.isNaN(parsed.getTime())) {
      throw new DiffError(path, `${JSON.stringify(actual)} is not an RFC3339 timestamp`);
    }
    return;
  }
  if (token === '$uuid$') {
    if (typeof actual !== 'string' || !UUID_RE.test(actual)) {
      throw new DiffError(path, `${JSON.stringify(actual)} is not a UUID`);
    }
    return;
  }
  if (token === '$int$') {
    if (typeof actual === 'boolean') {
      throw new DiffError(path, '$int$ expects integer, got boolean');
    }
    if (typeof actual !== 'number' || !Number.isInteger(actual)) {
      throw new DiffError(path, `$int$ expects integer, got ${JSON.stringify(actual)}`);
    }
    return;
  }
  throw new DiffError(path, `unknown wildcard ${JSON.stringify(token)}`);
}

export function resolveVars(value, vars) {
  if (typeof value === 'string') {
    if (value.startsWith('$vars.')) {
      const key = value.slice('$vars.'.length);
      return Object.prototype.hasOwnProperty.call(vars, key) ? vars[key] : '';
    }
    return value;
  }
  if (Array.isArray(value)) {
    return value.map((v) => resolveVars(v, vars));
  }
  if (value !== null && typeof value === 'object') {
    const out = {};
    for (const [k, v] of Object.entries(value)) {
      out[k] = resolveVars(v, vars);
    }
    return out;
  }
  return value;
}

export function selectJSONPath(root, expr) {
  if (!expr.startsWith('$')) {
    throw new DiffError(expr, 'path must start with $');
  }
  if (expr === '$') return root;
  const parts = expr.slice(1).split('.');
  let cur = root;
  for (const raw of parts) {
    if (!raw) continue;
    const bracket = raw.indexOf('[');
    if (bracket >= 0 && raw.endsWith(']')) {
      const name = raw.slice(0, bracket);
      const idxStr = raw.slice(bracket + 1, -1);
      if (name) {
        if (cur === null || typeof cur !== 'object' || Array.isArray(cur)) {
          throw new DiffError(raw, `cannot index ${name} on ${typeof cur}`);
        }
        cur = cur[name];
      }
      if (!Array.isArray(cur)) {
        throw new DiffError(raw, 'not an array');
      }
      const idx = Number.parseInt(idxStr, 10);
      if (Number.isNaN(idx)) throw new DiffError(raw, `bad array index ${idxStr}`);
      if (idx < 0 || idx >= cur.length) {
        throw new DiffError(raw, `index ${idx} out of range (len=${cur.length})`);
      }
      cur = cur[idx];
      continue;
    }
    if (cur === null || typeof cur !== 'object' || Array.isArray(cur)) {
      throw new DiffError(raw, `cannot descend into ${raw} on ${typeof cur}`);
    }
    cur = cur[raw];
  }
  return cur;
}

export function inferErrorStatus(className) {
  switch (className) {
    case 'AuthenticationError': return 401;
    case 'AuthorizationError':  return 403;
    case 'NotFoundError':       return 404;
    case 'ValidationError':     return 400;
    case 'ConflictError':       return 409;
    case 'RateLimitError':      return 429;
    case 'ServerError':         return 500;
    default:                    return 0;
  }
}
