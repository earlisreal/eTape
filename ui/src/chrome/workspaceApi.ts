import { WorkspaceService } from "../gen/wails/github.com/earlisreal/eTape/engine/internal/uiapi/index.js";
import type * as Generated from "../gen/wails/github.com/earlisreal/eTape/engine/internal/uiapi/models.js";

export type WorkspaceStatus = "accepted" | "blocked";

export interface WorkspaceCatalogEntry {
  workspaceId: string;
  name: string;
  open: boolean;
}

export interface WorkspaceCatalog {
  revision: number;
  entries: WorkspaceCatalogEntry[];
  openWorkspaceIds: string[];
}

export interface WorkspaceMutationResult {
  status: WorkspaceStatus;
  reason?: string;
  workspaceId?: string;
  revision: number;
  catalogRevision: number;
  entries: WorkspaceCatalogEntry[];
  openWorkspaceIds: string[];
}

export interface WorkspaceDocumentResult {
  status: WorkspaceStatus;
  reason?: string;
  workspaceId: string;
  revision: number;
  document?: unknown;
}

export interface WorkspaceFlushResult {
  status: WorkspaceStatus;
  reason?: string;
}

export interface WorkspaceApi {
  getCatalog(): Promise<WorkspaceCatalog>;
  create(args: { workspaceId: string; name: string; document?: unknown; expectedCatalogRevision?: number }): Promise<WorkspaceMutationResult>;
  rename(args: { workspaceId: string; name: string; expectedCatalogRevision?: number }): Promise<WorkspaceMutationResult>;
  remove(args: { workspaceId: string; expectedCatalogRevision?: number }): Promise<WorkspaceMutationResult>;
  load(workspaceId: string): Promise<WorkspaceDocumentResult>;
  save(args: { workspaceId: string; document: unknown; expectedRevision?: number }): Promise<WorkspaceDocumentResult>;
  open(workspaceId: string): Promise<WorkspaceMutationResult>;
  focus(workspaceId: string): Promise<WorkspaceMutationResult>;
  close(workspaceId: string): Promise<WorkspaceMutationResult>;
  completeClose?(args: { workspaceId: string; requestId: string }): Promise<WorkspaceMutationResult>;
  flush(): Promise<WorkspaceFlushResult>;
}

export interface LegacyWorkspaceClient {
  sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }>;
}

export function makeWorkspaceApi(wails: boolean, client: LegacyWorkspaceClient): WorkspaceApi {
  return wails ? wailsWorkspaceApi() : legacyWorkspaceApi(client);
}

function wailsWorkspaceApi(): WorkspaceApi {
  return {
    getCatalog: async () => catalog(await WorkspaceService.GetWorkspaceCatalog()),
    create: async (args) => mutation(await WorkspaceService.CreateWorkspace(args as Generated.CreateWorkspaceArgs)),
    rename: async (args) => mutation(await WorkspaceService.RenameWorkspace(args)),
    remove: async (args) => mutation(await WorkspaceService.DeleteWorkspace(args)),
    load: async (workspaceId) => document(await WorkspaceService.LoadWorkspace({ workspaceId })),
    save: async (args) => document(await WorkspaceService.SaveWorkspace(args as Generated.SaveWorkspaceArgs)),
    open: async (workspaceId) => mutation(await WorkspaceService.OpenWorkspace({ workspaceId })),
    focus: async (workspaceId) => mutation(await WorkspaceService.FocusWorkspace({ workspaceId })),
    close: async (workspaceId) => mutation(await WorkspaceService.CloseWorkspace({ workspaceId })),
    completeClose: async (args) => mutation(await WorkspaceService.CompleteWorkspaceClose(args as Generated.WorkspaceCloseArgs)),
    flush: async () => flush(await WorkspaceService.FlushWorkspace()),
  };
}

function legacyWorkspaceApi(client: LegacyWorkspaceClient): WorkspaceApi {
  return {
    getCatalog: async () => {
      const ack = await client.sendCommand("GetConfig", { key: "windows.v1" });
      const value = ack.status === "accepted" && isCatalog(ack.value) ? ack.value : { version: 1, entries: [] };
      return { revision: 0, entries: value.entries.map((entry) => ({ workspaceId: entry.id, name: entry.name, open: false })), openWorkspaceIds: [] };
    },
    create: async ({ workspaceId, name, document }) => {
      const current = await legacyWorkspaceApi(client).getCatalog();
      const entries = [...current.entries.filter((entry) => entry.workspaceId !== "monitoring"), { workspaceId, name, open: false }];
      const catalogAck = await client.sendCommand("SetConfig", { key: "windows.v1", value: { version: 1, entries: entries.map((entry) => ({ id: entry.workspaceId, name: entry.name })) } });
      if (catalogAck.status !== "accepted") return blocked(catalogAck.reason);
      const documentAck = await client.sendCommand("SetConfig", { key: `workspace.${workspaceId}`, value: document ?? blankDocument(workspaceId) });
      return documentAck.status === "accepted" ? acceptedMutation() : blocked(documentAck.reason);
    },
    rename: async ({ workspaceId, name }) => {
      const current = await legacyWorkspaceApi(client).getCatalog();
      const entries = current.entries.map((entry) => entry.workspaceId === workspaceId ? { ...entry, name } : entry);
      const ack = await client.sendCommand("SetConfig", { key: "windows.v1", value: { version: 1, entries: entries.filter((entry) => entry.workspaceId !== "monitoring").map((entry) => ({ id: entry.workspaceId, name: entry.name })) } });
      return ack.status === "accepted" ? acceptedMutation() : blocked(ack.reason);
    },
    remove: async ({ workspaceId }) => {
      const current = await legacyWorkspaceApi(client).getCatalog();
      const ack = await client.sendCommand("DeleteConfig", { key: `workspace.${workspaceId}` });
      if (ack.status !== "accepted") return blocked(ack.reason);
      await client.sendCommand("SetConfig", { key: "windows.v1", value: { version: 1, entries: current.entries.filter((entry) => entry.workspaceId !== workspaceId && entry.workspaceId !== "monitoring").map((entry) => ({ id: entry.workspaceId, name: entry.name })) } });
      return acceptedMutation();
    },
    load: async (workspaceId) => {
      const ack = await client.sendCommand("GetConfig", { key: `workspace.${workspaceId}` });
      if (ack.status !== "accepted") return blockedDocument(workspaceId, ack.reason);
      return ack.value == null ? blockedDocument(workspaceId, "workspace document is missing") : { status: "accepted", workspaceId, revision: 0, document: ack.value };
    },
    save: async ({ workspaceId, document }) => {
      const ack = await client.sendCommand("SetConfig", { key: `workspace.${workspaceId}`, value: document });
      return ack.status === "accepted" ? { status: "accepted", workspaceId, revision: 0, document } : blockedDocument(workspaceId, ack.reason);
    },
    open: async () => acceptedMutation(),
    focus: async () => acceptedMutation(),
    close: async () => acceptedMutation(),
    flush: async () => ({ status: "accepted" }),
  };
}

function catalog(value: Generated.WorkspaceCatalogResult): WorkspaceCatalog {
  return { revision: value.revision, entries: value.entries ?? [], openWorkspaceIds: value.openWorkspaceIds ?? [] };
}

function mutation(value: Generated.WorkspaceMutationResult): WorkspaceMutationResult {
  const result: WorkspaceMutationResult = {
    status: String(value.status) as WorkspaceStatus,
    revision: value.revision,
    catalogRevision: value.catalogRevision,
    entries: value.entries ?? [],
    openWorkspaceIds: value.openWorkspaceIds ?? [],
  };
  if (value.reason !== undefined) result.reason = value.reason;
  if (value.workspaceId !== undefined) result.workspaceId = value.workspaceId;
  return result;
}

function document(value: Generated.WorkspaceDocumentResult): WorkspaceDocumentResult {
  const result: WorkspaceDocumentResult = { status: String(value.status) as WorkspaceStatus, workspaceId: value.workspaceId, revision: value.revision };
  if (value.reason !== undefined) result.reason = value.reason;
  if (value.document != null) result.document = value.document;
  return result;
}

function acceptedMutation(): WorkspaceMutationResult {
  return { status: "accepted", revision: 0, catalogRevision: 0, entries: [], openWorkspaceIds: [] };
}

function flush(value: Generated.WorkspaceFlushResult): WorkspaceFlushResult {
  const result: WorkspaceFlushResult = { status: String(value.status) as WorkspaceStatus };
  if (value.reason !== undefined) result.reason = value.reason;
  return result;
}

function blocked(reason?: string): WorkspaceMutationResult {
  const result: WorkspaceMutationResult = { status: "blocked", revision: 0, catalogRevision: 0, entries: [], openWorkspaceIds: [] };
  if (reason !== undefined) result.reason = reason;
  return result;
}

function blockedDocument(workspaceId: string, reason?: string): WorkspaceDocumentResult {
  const result: WorkspaceDocumentResult = { status: "blocked", workspaceId, revision: 0 };
  if (reason !== undefined) result.reason = reason;
  return result;
}

function blankDocument(workspaceId: string): Record<string, unknown> {
  return { name: workspaceId, layoutVersion: 8, panels: [], layout: null };
}

function isCatalog(value: unknown): value is { version: 1; entries: Array<{ id: string; name: string }> } {
  if (!value || typeof value !== "object") return false;
  const candidate = value as { version?: unknown; entries?: unknown };
  return candidate.version === 1 && Array.isArray(candidate.entries)
    && candidate.entries.every((entry) => !!entry && typeof entry === "object" && typeof (entry as { id?: unknown }).id === "string" && typeof (entry as { name?: unknown }).name === "string");
}
