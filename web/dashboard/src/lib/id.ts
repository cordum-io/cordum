export function newID(prefix?: string): string {
  const cryptoObj = globalThis.crypto as Crypto | undefined;
  const id = cryptoObj?.randomUUID?.() ?? `${Date.now().toString(16)}-${Math.random().toString(16).slice(2)}`;
  return prefix ? `${prefix}${id}` : id;
}

