export type UiLogFields = Record<string, unknown>;

let debugEnabled = false;

export function initUiLogFromQuery(search: string): void {
  debugEnabled = new URLSearchParams(search).get("debug") === "1";
}

function write(method: "debug" | "info" | "warn" | "error", event: string, fields?: UiLogFields): void {
  const log = console[method] as (...args: unknown[]) => void;
  if (fields === undefined) log.call(console, "[ui]", event);
  else log.call(console, "[ui]", event, fields);
}

export const uiLog = {
  debug: (event: string, fields?: UiLogFields): void => {
    if (debugEnabled) write("debug", event, fields);
  },
  info: (event: string, fields?: UiLogFields): void => write("info", event, fields),
  warn: (event: string, fields?: UiLogFields): void => write("warn", event, fields),
  error: (event: string, fields?: UiLogFields): void => write("error", event, fields),
};
