import test from 'node:test';
import assert from 'node:assert/strict';

import {
  diff,
  DiffError,
  resolveVars,
  selectJSONPath,
  inferErrorStatus,
} from '../src/diff.mjs';

test('diff: primitives and wildcards', () => {
  const cases = [
    ['hello', 'hello', true],
    ['hello', 'world', false],
    ['whatever', '$any$', true],
    [42, '$any$', true],
    [{ k: 'v' }, '$any$', true],
    ['2026-01-01T00:00:00Z', '$timestamp$', true],
    ['not-a-date', '$timestamp$', false],
    [42, '$int$', true],
    [3.14, '$int$', false],
    ['550e8400-e29b-41d4-a716-446655440000', '$uuid$', true],
    ['not-a-uuid', '$uuid$', false],
    ['x', '$zzz$', false],
  ];
  for (const [actual, expected, wantPass] of cases) {
    let passed;
    try {
      diff(actual, expected, '$');
      passed = true;
    } catch (err) {
      if (!(err instanceof DiffError)) throw err;
      passed = false;
    }
    assert.equal(
      passed,
      wantPass,
      `diff(${JSON.stringify(actual)}, ${JSON.stringify(expected)}) => passed=${passed}, want ${wantPass}`,
    );
  }
});

test('diff: nested object tolerates extra keys', () => {
  const actual = { id: 'abc', name: 'alpha', status: 'active', extra: 'ignored' };
  const expected = { name: 'alpha', status: 'active', id: '$any$' };
  assert.doesNotThrow(() => diff(actual, expected, '$'));
});

test('diff: array length mismatch rejects', () => {
  assert.throws(() => diff(['a', 'b', 'c'], ['a', 'b'], '$'), DiffError);
});

test('diff: object missing key rejects', () => {
  assert.throws(() => diff({ a: 1 }, { a: 1, b: '$any$' }, '$'), DiffError);
});

test('diff: int wildcard rejects boolean', () => {
  assert.throws(() => diff(true, '$int$', '$'), DiffError);
});

test('selectJSONPath: covers nested + arrays', () => {
  const root = {
    id: 'xyz',
    items: [{ id: 'item-0' }, { id: 'item-1' }],
  };
  assert.equal(selectJSONPath(root, '$.id'), 'xyz');
  assert.equal(selectJSONPath(root, '$.items[0].id'), 'item-0');
  assert.equal(selectJSONPath(root, '$.items[1].id'), 'item-1');
});

test('resolveVars: substitutes nested', () => {
  const vars = { agentId: 'agent-0001', tenant: 'default' };
  const out = resolveVars(
    {
      id: '$vars.agentId',
      tenant: '$vars.tenant',
      nested: { copy: '$vars.agentId' },
      list: ['$vars.agentId', 'literal'],
    },
    vars,
  );
  assert.equal(out.id, 'agent-0001');
  assert.equal(out.nested.copy, 'agent-0001');
  assert.deepEqual(out.list, ['agent-0001', 'literal']);
});

test('inferErrorStatus: taxonomy matches Go + Python harnesses', () => {
  assert.equal(inferErrorStatus('AuthenticationError'), 401);
  assert.equal(inferErrorStatus('AuthorizationError'), 403);
  assert.equal(inferErrorStatus('NotFoundError'), 404);
  assert.equal(inferErrorStatus('ValidationError'), 400);
  assert.equal(inferErrorStatus('ConflictError'), 409);
  assert.equal(inferErrorStatus('RateLimitError'), 429);
  assert.equal(inferErrorStatus('ServerError'), 500);
  assert.equal(inferErrorStatus('NetworkError'), 0);
  assert.equal(inferErrorStatus('TimeoutError'), 0);
});
