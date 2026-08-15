const WORKSPACE_ID_RE = /^[a-z0-9-]{1,64}$/;

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

export function openWorkspaceWindow(id: string): Window | null {
  return window.open(workspaceUrl(id), workspaceWindowTarget(id));
}

/** Lowest free `window-N` (N starts at 2; `main` is window 1). */
export function nextWindowName(existing: string[]): string {
  const taken = new Set(existing);
  for (let n = 2; ; n++) {
    const candidate = `window-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
}
