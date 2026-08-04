export interface CommandClient { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }> }
export interface WindowCatalogV1 { version: 1; entries: Array<{ id: string; name: string }> }
export const WINDOW_CATALOG_KEY = "windows.v1";
export const emptyWindows = (): WindowCatalogV1 => ({ version: 1, entries: [] });

export function validateName(name: string, existing: string[], reserved: string[] = []): string {
  const value = name.trim();
  if (!value || value.length > 64 || /[\x00-\x1f\x7f]/.test(value)) throw new Error("Name must be 1–64 characters with no control characters.");
  if ([...existing, ...reserved].some((n) => n.toLocaleLowerCase() === value.toLocaleLowerCase())) throw new Error("That name is already in use.");
  return value;
}

async function read<T>(client: CommandClient, key: string, fallback: T, valid: (v: unknown) => v is T): Promise<T> {
  const ack = await client.sendCommand("GetConfig", { key });
  return ack.status === "accepted" && valid(ack.value) ? ack.value : fallback;
}
const windowsValid = (v: unknown): v is WindowCatalogV1 => !!v && typeof v === "object" && (v as WindowCatalogV1).version === 1 && Array.isArray((v as WindowCatalogV1).entries) && (v as WindowCatalogV1).entries.every((e) => e && typeof e.id === "string" && typeof e.name === "string");
export const readWindows = (c: CommandClient) => read(c, WINDOW_CATALOG_KEY, emptyWindows(), windowsValid);

export async function withLock<T>(name: string, fn: () => Promise<T>): Promise<T> {
  return navigator.locks ? navigator.locks.request(name, fn) : fn();
}
export async function mutateWindows(client: CommandClient, change: (c: WindowCatalogV1) => WindowCatalogV1): Promise<WindowCatalogV1> {
  return withLock("etape.window-catalog", async () => {
    const next = change(await readWindows(client));
    const ack = await client.sendCommand("SetConfig", { key: WINDOW_CATALOG_KEY, value: next });
    if (ack.status !== "accepted") throw new Error(ack.reason ?? "Could not save window catalog.");
    new BroadcastChannel("etape.window-catalog").postMessage("changed");
    return next;
  });
}
