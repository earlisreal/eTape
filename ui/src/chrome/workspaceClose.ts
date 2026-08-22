import type { Workspace } from "./workspace";

export const WORKSPACE_CLOSE_REQUESTED = "etape:workspace-close-requested";

export interface WorkspaceCloseRequest {
  workspaceId: string;
  requestId: string;
}

export function parseWorkspaceCloseRequest(value: unknown): WorkspaceCloseRequest | null {
  if (!value || typeof value !== "object") return null;
  const request = value as { workspaceId?: unknown; requestId?: unknown };
  return typeof request.workspaceId === "string" && typeof request.requestId === "string" && request.workspaceId !== "" && request.requestId !== ""
    ? { workspaceId: request.workspaceId, requestId: request.requestId }
    : null;
}

export async function completeDurableWorkspaceClose(args: {
  workspaceId: string;
  requestId: string;
  getCurrentDocument: () => Workspace | null;
  save: (document: Workspace) => void | Promise<void>;
  flush: () => Promise<void>;
  complete: (args: { workspaceId: string; requestId: string }) => Promise<{ status: string; reason?: string }>;
}): Promise<void> {
  const document = args.getCurrentDocument();
  if (!document) throw new Error("Workspace is not ready to close durably.");
  await args.save(document);
  await args.flush();
  const result = await args.complete({ workspaceId: args.workspaceId, requestId: args.requestId });
  if (String(result.status) !== "accepted") throw new Error(result.reason ?? "Could not complete workspace close.");
}
