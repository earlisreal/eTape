export interface WindowStateWindow {
  readonly screenX: number;
  readonly screenY: number;
  readonly outerWidth: number;
  readonly outerHeight: number;
  moveTo(x: number, y: number): void;
  resizeTo(width: number, height: number): void;
  addEventListener(type: "resize", listener: () => void): void;
  removeEventListener(type: "resize", listener: () => void): void;
}

interface WindowStateClient {
  sendCommand(name: string, args: unknown): Promise<unknown>;
  onState?(callback: (state: string) => void): (() => void) | void;
}

export interface WindowStateBounds {
  workspaceId: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

const RESIZE_DEBOUNCE_MS = 150;
const POSITION_POLL_MS = 1000;

function restoredBounds(ack: unknown, workspaceId: string): WindowStateBounds | undefined {
  if (!ack || typeof ack !== "object" || !("value" in ack)) return undefined;
  const value = ack.value;
  if (!value || typeof value !== "object") return undefined;
  const bounds = value as Partial<WindowStateBounds>;
  if (bounds.workspaceId !== workspaceId || ![bounds.x, bounds.y, bounds.width, bounds.height].every(Number.isFinite)) return undefined;
  return bounds as WindowStateBounds;
}

export function readWindowState(workspaceId: string, win: WindowStateWindow = window): WindowStateBounds {
  return {
    workspaceId,
    x: Math.round(win.screenX),
    y: Math.round(win.screenY),
    width: Math.round(win.outerWidth),
    height: Math.round(win.outerHeight),
  };
}

function sameBounds(a: WindowStateBounds | undefined, b: WindowStateBounds): boolean {
  return a?.workspaceId === b.workspaceId && a.x === b.x && a.y === b.y
    && a.width === b.width && a.height === b.height;
}

export function trackWindowState(workspaceId: string, client: WindowStateClient, win: WindowStateWindow = window): () => void {
  let lastSent: WindowStateBounds | undefined;
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  let opened = false;

  const send = (force = false, restore = false): void => {
    const next = readWindowState(workspaceId, win);
    if (!force && sameBounds(lastSent, next)) return;
    lastSent = next;
    const sent = client.sendCommand("SetWindowState", next);
    if (!restore) return void sent;
    void sent.then((ack) => {
      const saved = restoredBounds(ack, workspaceId);
      if (!saved) return;
      if (workspaceId === "main") {
        lastSent = undefined;
        send(true);
        return;
      }
      try {
        win.resizeTo(saved.width, saved.height);
        win.moveTo(saved.x, saved.y);
      } catch {
        // Chromium may reject window controls; keep the persisted bounds for the next launch.
      }
    });
  };
  const onResize = (): void => {
    if (resizeTimer !== undefined) clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => { resizeTimer = undefined; send(); }, RESIZE_DEBOUNCE_MS);
  };
  const onState = (state: string): void => {
    if (state !== "open") return;
    if (!opened) { opened = true; return; }
    lastSent = undefined;
    send(true);
  };

  const removeState = client.onState?.(onState);
  send(true, true);
  win.addEventListener("resize", onResize);
  const poll = setInterval(send, POSITION_POLL_MS);
  return () => {
    if (resizeTimer !== undefined) clearTimeout(resizeTimer);
    clearInterval(poll);
    win.removeEventListener("resize", onResize);
    removeState?.();
  };
}
