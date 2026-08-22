import { blankWorkspace } from "./workspace";
import type { WorkspaceApi } from "./workspaceApi";

export interface CommandClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }>;
  workspace?: WorkspaceApi;
}

export interface WindowCatalogV1 {
  version: 1;
  revision?: number;
  entries: Array<{ id: string; name: string }>;
}

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

const windowsValid = (v: unknown): v is WindowCatalogV1 => !!v && typeof v === "object"
  && (v as WindowCatalogV1).version === 1
  && Array.isArray((v as WindowCatalogV1).entries)
  && (v as WindowCatalogV1).entries.every((e) => e && typeof e.id === "string" && typeof e.name === "string");

export async function readWindows(client: CommandClient): Promise<WindowCatalogV1> {
  if (client.workspace) {
    const catalog = await client.workspace.getCatalog();
    return {
      version: 1,
      revision: catalog.revision,
      entries: catalog.entries.map((entry) => ({ id: entry.workspaceId, name: entry.name })),
    };
  }
  return read(client, WINDOW_CATALOG_KEY, emptyWindows(), windowsValid);
}

// Browser fallback is single-page only; native workspace coordination lives in Go.
let catalogQueue = Promise.resolve();
export function withLock<T>(_name: string, fn: () => Promise<T>): Promise<T> {
  const result = catalogQueue.then(fn, fn);
  catalogQueue = result.then(() => undefined, () => undefined);
  return result;
}

export async function mutateWindows(client: CommandClient, change: (catalog: WindowCatalogV1) => WindowCatalogV1): Promise<WindowCatalogV1> {
  return withLock("etape.window-catalog", async () => {
    const current = await readWindows(client);
    const next = change(current);
    if (!client.workspace) {
      const ack = await client.sendCommand("SetConfig", { key: WINDOW_CATALOG_KEY, value: next });
      if (ack.status !== "accepted") throw new Error(ack.reason ?? "Could not save window catalog.");
      return next;
    }

    let revision = current.revision ?? 0;
    const currentByID = new Map(current.entries.map((entry) => [entry.id, entry]));
    const nextByID = new Map(next.entries.map((entry) => [entry.id, entry]));
    for (const entry of current.entries) {
      if (nextByID.has(entry.id)) continue;
      const result = await client.workspace.remove({ workspaceId: entry.id, expectedCatalogRevision: revision });
      if (result.status !== "accepted") throw new Error(result.reason ?? "Could not delete workspace.");
      revision = result.catalogRevision;
    }
    for (const entry of next.entries) {
      const previous = currentByID.get(entry.id);
      if (!previous) {
        const result = await client.workspace.create({
          workspaceId: entry.id,
          name: entry.name,
          document: blankWorkspace(entry.id),
          expectedCatalogRevision: revision,
        });
        if (result.status !== "accepted") throw new Error(result.reason ?? "Could not create workspace.");
        revision = result.catalogRevision;
      } else if (previous.name !== entry.name) {
        const result = await client.workspace.rename({ workspaceId: entry.id, name: entry.name, expectedCatalogRevision: revision });
        if (result.status !== "accepted") throw new Error(result.reason ?? "Could not rename workspace.");
        revision = result.catalogRevision;
      }
    }
    return readWindows(client);
  });
}
