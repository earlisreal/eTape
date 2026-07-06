import type { ClientMessage, ServerMessage } from "./contract";

const SERVER_KINDS = new Set(["snapshot", "delta", "ack", "pong", "result"]);

// Never throws: a malformed or unknown frame yields null so the reader loop
// can drop-and-count rather than crash (honesty policy + burst tolerance).
export function decodeServerMessage(raw: string): ServerMessage | null {
  let obj: unknown;
  try {
    obj = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof obj !== "object" || obj === null) return null;
  const kind = (obj as { kind?: unknown }).kind;
  if (typeof kind !== "string" || !SERVER_KINDS.has(kind)) return null;
  return obj as ServerMessage;
}

export function encodeClientMessage(msg: ClientMessage): string {
  return JSON.stringify(msg);
}
