const NAME_RE = /^[a-z0-9-]{1,32}$/;

/** Parse `?workspace=<name>`; default `main`; reject anything not [a-z0-9-]. */
export function parseWorkspaceName(search: string): string {
  const raw = new URLSearchParams(search).get("workspace");
  if (!raw) return "main";
  const name = raw.toLowerCase();
  return NAME_RE.test(name) ? name : "main";
}

/** Lowest free `window-N` (N starts at 2; `main` is window 1). */
export function nextWindowName(existing: string[]): string {
  const taken = new Set(existing);
  for (let n = 2; ; n++) {
    const candidate = `window-${n}`;
    if (!taken.has(candidate)) return candidate;
  }
}
