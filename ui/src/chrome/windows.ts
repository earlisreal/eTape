const WORKSPACE_ID_RE = /^[a-z0-9-]{1,64}$/;
const WORKSPACE_WINDOW_POPUP = "popup=yes";
const NEWS_WINDOW_TARGET = "etape-news-reader";
const NEWS_WINDOW_WIDTH = 1100;
const NEWS_WINDOW_HEIGHT = 800;

let newsWindow: Window | null = null;

function navigateNewsWindow(url: string): void {
  const link = document.createElement("a");
  link.href = url;
  link.target = NEWS_WINDOW_TARGET;
  link.referrerPolicy = "no-referrer";
  link.click();
}

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

export function openWorkspaceWindow(id: string): Window | null {
  return window.open(workspaceUrl(id), workspaceWindowTarget(id), workspaceWindowFeatures());
}

/** Focus the main workspace without reloading an existing window. */
export function focusMainWorkspace(): void {
  if (parseWorkspaceName(window.location.search) === "main") return;
  const main = window.open("", workspaceWindowTarget("main"), workspaceWindowFeatures());
  if (!main) return;
  try {
    if (main.location.href === "about:blank") main.location.href = workspaceUrl("main");
    main.focus();
  } catch {
    // Browsers may reject window controls; symbol activation still succeeds.
  }
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
    navigateNewsWindow(url);
    newsWindow.focus();
    return newsWindow;
  }

  const width = Math.min(NEWS_WINDOW_WIDTH, Math.floor((window.screen.availWidth || NEWS_WINDOW_WIDTH) * 0.8));
  const height = Math.min(NEWS_WINDOW_HEIGHT, Math.floor((window.screen.availHeight || NEWS_WINDOW_HEIGHT) * 0.8));
  const left = Math.round(window.screenX + (window.outerWidth - width) / 2);
  const top = Math.round(window.screenY + (window.outerHeight - height) / 2);
  newsWindow = window.open("about:blank", NEWS_WINDOW_TARGET, [
    "popup=yes",
    `width=${width}`,
    `height=${height}`,
    `left=${left}`,
    `top=${top}`,
    "resizable=yes",
    "scrollbars=yes",
  ].join(","));
  try {
    if (newsWindow) {
      newsWindow.opener = null;
      newsWindow.resizeTo(width, height);
      newsWindow.moveTo(left, top);
    }
  } catch {
    // Browsers may reject window controls; the requested popup bounds still apply.
  }
  if (newsWindow) navigateNewsWindow(url);
  newsWindow?.focus();
  return newsWindow;
}

export function closeNewsWindow(): void {
  const popup = newsWindow;
  newsWindow = null;
  if (!popup) return;
  try {
    if (!popup.closed) popup.close();
  } catch {
    // Browsers may reject controls on a cross-origin popup.
  }
}

/** Lowest free `window-N` (N starts at 2; `main` is window 1). */
export function nextWindowName(existing: string[]): string {
  const taken = new Set(existing);
  for (let n = 2; ; n++) {
    const candidate = `window-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
}
