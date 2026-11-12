import { describe, expect, it } from 'vitest';
import {
  LOST_CONSOLE_LOG_AT_TEARDOWN,
  isLostConsoleLogAtTeardown,
} from './vitestTeardown';

const NAME = 'EnvironmentTeardownError';
const message = (method: string) =>
  `[vitest-worker]: Closing rpc while "${method}" was pending`;

describe('isLostConsoleLogAtTeardown', () => {
  it('exposes the exact vitest message', () => {
    expect(LOST_CONSOLE_LOG_AT_TEARDOWN).toBe(message('onUserConsoleLog'));
  });

  it('matches the serialized error vitest reports', () => {
    expect(
      isLostConsoleLogAtTeardown({
        name: NAME,
        message: message('onUserConsoleLog'),
        type: 'Unhandled Rejection',
      }),
    ).toBe(true);
  });

  it('matches an Error instance', () => {
    const error = new Error(message('onUserConsoleLog'));
    error.name = NAME;
    expect(isLostConsoleLogAtTeardown(error)).toBe(true);
  });

  it('rejects another RPC method pending at teardown (onTaskUpdate)', () => {
    expect(
      isLostConsoleLogAtTeardown({ name: NAME, message: message('onTaskUpdate') }),
    ).toBe(false);
  });

  it('rejects another RPC method pending at teardown (fetch)', () => {
    expect(
      isLostConsoleLogAtTeardown({ name: NAME, message: message('fetch') }),
    ).toBe(false);
  });

  it('rejects the exact message under a different name', () => {
    expect(
      isLostConsoleLogAtTeardown({ name: 'Error', message: message('onUserConsoleLog') }),
    ).toBe(false);
  });

  it('rejects the message with a suffix', () => {
    expect(
      isLostConsoleLogAtTeardown({
        name: NAME,
        message: `${message('onUserConsoleLog')} (retry)`,
      }),
    ).toBe(false);
  });

  it('rejects the message with a prefix', () => {
    expect(
      isLostConsoleLogAtTeardown({
        name: NAME,
        message: `(retry) ${message('onUserConsoleLog')}`,
      }),
    ).toBe(false);
  });

  it('rejects null', () => {
    expect(isLostConsoleLogAtTeardown(null)).toBe(false);
  });

  it('rejects undefined', () => {
    expect(isLostConsoleLogAtTeardown(undefined)).toBe(false);
  });

  it('rejects a string', () => {
    expect(isLostConsoleLogAtTeardown(message('onUserConsoleLog'))).toBe(false);
  });

  it('rejects an empty object', () => {
    expect(isLostConsoleLogAtTeardown({})).toBe(false);
  });
});
