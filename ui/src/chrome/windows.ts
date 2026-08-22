import { Call } from "@wailsio/runtime";

const WORKSPACE_ID_RE = /^[a-z0-9-]{1,64}$/;
const WORKSPACE_WINDOW_POPUP = "popup=yes";
const NEWS_WINDOW_TARGET = "etape-news-reader";
const NEWS_WINDOW_WIDTH = 1100;
const NEWS_WINDOW_HEIGHT = 800;
const OPEN_WORKSPACE_METHOD = "github.com/earlisreal/eTape/engine/internal/desktop.Host.OpenWorkspace";

let newsWindow: Window | null = null;

/** Parse `?workspace=<id>`; default `main`; accepts catalog UUIDs. */
export function parseWorkspaceName(search: string): string {
  const raw = new URLSearchParams(search).get("workspace");
  if (!raw) return "main";
  const name = raw.toLowerCase();
  return WORKSPACE_ID_RE.test(name) ? name : "main";
}

export function workspaceWindowTarget(id: string): string {
  return `etape-workspace-${id}`;
}

export function isNativeWindow(): boolean {
  const candidate = window as typeof window & { chrome?: { webview?: unknown }; _wails?: { environment?: { OS?: string } } };
  return !!candidate.chrome?.webview || candidate._wails?.environment?.OS === "windows";
}

export function workspaceUrl(id: string, href = window.location.href): string {
  const target = new URL(href);
  target.search = `?workspace=${encodeURIComponent(id)}`;
  target.hash = "";
  return target.href;
}

export function workspaceWindowFeatures(): string {
  const { availWidth, availHeight } = window.screen;
  const width = availWidth || window.innerWidth;
  const height = availHeight || window.innerHeight;
  if (width <= 0 || height <= 0) return WORKSPACE_WINDOW_POPUP;
  return `${WORKSPACE_WINDOW_POPUP},width=${width},height=${height}`;
}

export function openWorkspaceWindowNative(id: string): Promise<void> {
  return Call.ByName(OPEN_WORKSPACE_METHOD, id).then(() => undefined);
}

export function openWorkspaceWindow(id: string): Window | null {
  if (isNativeWindow()) {
    void openWorkspaceWindowNative(id).catch(() => {});
    return null;
  }
  return window.open(workspaceUrl(id), workspaceWindowTarget(id), workspaceWindowFeatures());
}

export function openNewsWindow(url: string): Window | null {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return null;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return null;

  if (newsWindow && !newsWindow.closed) {
    newsWindow.location.href = url;
    newsWindow.focus();
    return newsWindow;
  }

  const left = Math.round(window.screenX + (window.outerWidth - NEWS_WINDOW_WIDTH) / 2);
  const top = Math.round(window.screenY + (window.outerHeight - NEWS_WINDOW_HEIGHT) / 2);
  newsWindow = window.open(url, NEWS_WINDOW_TARGET, [
    "popup=yes",
    `width=${NEWS_WINDOW_WIDTH}`,
    `height=${NEWS_WINDOW_HEIGHT}`,
    `left=${left}`,
    `top=${top}`,
    "resizable=yes",
    "scrollbars=yes",
    "noopener",
    "noreferrer",
  ].join(","));
  newsWindow?.focus();
  return newsWindow;
}

/** Lowest free `window-N` (N starts at 2; `main` is window 1). */
export function nextWindowName(existing: string[]): string {
  const taken = new Set(existing);
  for (let n = 2; ; n++) {
    const candidate = `window-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
}
