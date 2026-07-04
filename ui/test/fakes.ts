import type { ISocket } from "../src/wire/WsClient";
import type { RafLike } from "../src/render/surface";

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

export class FakeRaf implements RafLike {
  private cbs = new Map<number, () => void>();
  private id = 0;
  request(cb: () => void): number { const id = ++this.id; this.cbs.set(id, cb); return id; }
  cancel(id: number): void { this.cbs.delete(id); }
  // test helper: run one frame (snapshots callbacks so re-registration lands next frame)
  tick(): void { const batch = [...this.cbs.values()]; this.cbs.clear(); batch.forEach((cb) => cb()); }
}
