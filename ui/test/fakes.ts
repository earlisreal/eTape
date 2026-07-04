import type { ISocket } from "../src/wire/WsClient";

export class FakeSocket implements ISocket {
  static instances: FakeSocket[] = [];
  sent: string[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((data: string) => void) | null = null;
  onclose: (() => void) | null = null;

  constructor(public url: string) {
    FakeSocket.instances.push(this);
  }
  send(data: string): void { this.sent.push(data); }
  close(): void { this.closed = true; this.onclose?.(); }

  // test helpers
  open(): void { this.onopen?.(); }
  emit(raw: string): void { this.onmessage?.(raw); }
  dropFromServer(): void { this.onclose?.(); }
  static last(): FakeSocket { return FakeSocket.instances[FakeSocket.instances.length - 1]; }
  static reset(): void { FakeSocket.instances = []; }
}
