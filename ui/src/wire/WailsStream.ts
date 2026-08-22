import { Stream } from "@wailsio/runtime";
import { RuntimeService } from "../gen/wails/github.com/earlisreal/eTape/engine/cmd/etape/index.js";
import type { ISocket, SocketCloseEvent } from "./WsClient";

export const WAILS_STREAM_NAME = "etape.runtime";
export const WAILS_STREAM_URL = `wails://${WAILS_STREAM_NAME}`;
const STREAM_PROTOCOL = 1;

interface WailsWindow extends Window {
  _wails?: { environment?: { OS?: string }; streamFactory?: unknown };
  chrome?: { webview?: unknown };
}

export interface WailsStreamLike {
  binaryType?: string;
  onopen: (() => void) | null;
  onmessage: ((event: { data: unknown }) => void) | null;
  onclose: ((event: { code?: number; reason?: string }) => void) | null;
  send(data: string): void;
  close(): void;
}

export type OpenStreamSession = (workspaceID: string) => PromiseLike<string>;
export type WailsStreamFactory = (name: string) => WailsStreamLike;

export function isWailsStreamAvailable(): boolean {
  if (typeof window === "undefined") return false;
  const candidate = window as WailsWindow;
  return typeof candidate._wails?.streamFactory === "function"
    || typeof candidate._wails?.environment?.OS === "string"
    || candidate.chrome?.webview !== undefined;
}

export function makeWailsSocketFactory(
  workspaceID: string,
  options: { openSession?: OpenStreamSession; streamFactory?: WailsStreamFactory } = {},
): (url: string) => ISocket {
  const openSession = options.openSession ?? ((id) => RuntimeService.OpenStreamSession(id));
  const streamFactory = options.streamFactory ?? ((name) => Stream(name) as unknown as WailsStreamLike);
  return () => new WailsSocket(workspaceID, openSession, streamFactory);
}

class WailsSocket implements ISocket {
  onopen: (() => void) | null = null;
  onmessage: ((data: string) => void) | null = null;
  onclose: ((event?: SocketCloseEvent) => void) | null = null;

  private stream: WailsStreamLike | null = null;
  private closed = false;
  private closeNotified = false;
  private applicationOpen = false;
  private readonly decoder = new TextDecoder();

  constructor(
    private readonly workspaceID: string,
    openSession: OpenStreamSession,
    private readonly streamFactory: WailsStreamFactory,
  ) {
    Promise.resolve(openSession(workspaceID)).then(
      (session) => this.openTransport(session),
      (error: unknown) => this.notifyClose(1006, errorMessage(error)),
    );
  }

  send(data: string): void {
    if (this.applicationOpen) this.stream?.send(data);
  }

  close(): void {
    this.closed = true;
    this.stream?.close();
  }

  private openTransport(session: string): void {
    if (this.closed) return;
    const stream = this.streamFactory(WAILS_STREAM_NAME);
    this.stream = stream;
    if (stream.binaryType !== undefined) stream.binaryType = "arraybuffer";
    stream.onopen = () => {
      if (this.closed) return;
      stream.send(JSON.stringify({
        protocol: STREAM_PROTOCOL,
        workspaceId: this.workspaceID,
        session,
      }));
    };
    stream.onmessage = (event) => this.receive(event.data);
    stream.onclose = (event) => {
      this.notifyClose(event.code ?? 1006, event.reason ?? "");
    };
  }

  private receive(data: unknown): void {
    if (typeof data === "string") {
      this.receiveText(data);
      return;
    }
    if (data instanceof ArrayBuffer) {
      this.receiveText(this.decoder.decode(data));
      return;
    }
    if (ArrayBuffer.isView(data)) {
      this.receiveText(this.decoder.decode(data));
      return;
    }
    if (typeof Blob !== "undefined" && data instanceof Blob) {
      void data.text().then((text) => this.receiveText(text), (error: unknown) => {
        this.notifyClose(1006, errorMessage(error));
      });
    }
  }

  private receiveText(text: string): void {
    let control: { type?: unknown; error?: unknown; reason?: unknown } | null = null;
    try {
      const value: unknown = JSON.parse(text);
      if (value !== null && typeof value === "object") {
        control = value as { type?: unknown; error?: unknown; reason?: unknown };
      }
    } catch {
      // WsClient owns malformed-frame handling for ordinary protocol frames.
    }

    switch (control?.type) {
      case "accepted":
        if (!this.closed && !this.applicationOpen) {
          this.applicationOpen = true;
          this.onopen?.();
        }
        return;
      case "rejected":
        this.notifyClose(1008, control.error === undefined ? "stream rejected" : String(control.error));
        this.stream?.close();
        return;
      case "stopping":
      case "restarting":
        this.notifyClose(
          control.type === "stopping" ? 1001 : 1000,
          String(control.reason ?? (control.type === "stopping" ? "engine stopped" : "restarting")),
        );
        this.stream?.close();
        return;
      default:
        if (this.applicationOpen && !this.closed) this.onmessage?.(text);
    }
  }

  private notifyClose(code: number, reason: string): void {
    if (this.closeNotified) return;
    this.closeNotified = true;
    this.closed = true;
    this.applicationOpen = false;
    this.onclose?.({ code, reason });
  }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
