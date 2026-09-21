export interface WindowStateWindow {
  readonly screenX: number;
  readonly screenY: number;
  readonly outerWidth: number;
  readonly outerHeight: number;
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

  const send = (force = false): void => {
    const next = readWindowState(workspaceId, win);
    if (!force && sameBounds(lastSent, next)) return;
    lastSent = next;
    void client.sendCommand("SetWindowState", next);
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
  send(true);
  win.addEventListener("resize", onResize);
  const poll = setInterval(send, POSITION_POLL_MS);
  return () => {
    if (resizeTimer !== undefined) clearTimeout(resizeTimer);
    clearInterval(poll);
    win.removeEventListener("resize", onResize);
    removeState?.();
  };
}
