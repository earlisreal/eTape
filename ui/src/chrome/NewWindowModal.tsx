import { useEffect, useState } from "react";
import { Button } from "./controls/Button";
import { mutateWindows, readWindows, validateName, type CommandClient, type WindowCatalogV1 } from "./catalogs";
import { useTheme } from "./ThemeProvider";
import { modalTracker } from "./modalTracker";
import { blankWorkspace, MONITORING_WORKSPACE_ID, MONITORING_WORKSPACE_NAME, type WorkspaceStore } from "./workspace";
import { isNativeWindow, openWorkspaceWindow, openWorkspaceWindowNative, workspaceUrl, workspaceWindowFeatures } from "./windows";

export function NewWindowModal({ open, currentId, commands, workspaceStore, onClose }: { open: boolean; currentId: string; commands: CommandClient; workspaceStore?: WorkspaceStore; onClose: () => void }): JSX.Element | null {
  const [catalog, setCatalog] = useState<WindowCatalogV1>({ version: 1, entries: [] });
  const [name, setName] = useState(""); const [error, setError] = useState("");
  const { palette } = useTheme();
  useEffect(() => {
    if (!open) return;
    let alive = true;
    const refresh = () => void readWindows(commands).then((next) => { if (alive) setCatalog(next); });
    const unwatch = workspaceStore?.watchCatalog(refresh);
    refresh();
    return () => { alive = false; unwatch?.(); };
  }, [open, commands, workspaceStore]);
  useEffect(() => {
    if (!open) return;
    modalTracker.setOpen(true);
    return () => modalTracker.setOpen(false);
  }, [open]);
  if (!open) return null;
  const entries = [
    { id: MONITORING_WORKSPACE_ID, name: MONITORING_WORKSPACE_NAME },
    ...catalog.entries.filter((entry) => entry.id !== MONITORING_WORKSPACE_ID),
  ];
  const create = async () => {
    setError(""); const native = isNativeWindow();
    const placeholder = native ? null : window.open("about:blank", "_blank", workspaceWindowFeatures());
    try {
      const clean = validateName(name, catalog.entries.map((e) => e.name), ["main", MONITORING_WORKSPACE_NAME]); const id = crypto.randomUUID();
      if (commands.workspace) {
        const created = await commands.workspace.create({ workspaceId: id, name: clean, document: blankWorkspace(id), expectedCatalogRevision: catalog.revision ?? 0 });
        if (created.status !== "accepted") throw new Error(created.reason ?? "Could not create empty workspace.");
        setCatalog(await readWindows(commands));
      } else {
        const next = await mutateWindows(commands, (fresh) => ({ ...fresh, entries: [...fresh.entries, { id, name: validateName(clean, fresh.entries.map((e) => e.name), ["main", MONITORING_WORKSPACE_NAME]) }] }));
        const saved = await commands.sendCommand("SetConfig", { key: `workspace.${id}`, value: blankWorkspace(id) });
        if (saved.status !== "accepted") {
          await mutateWindows(commands, (fresh) => ({ ...fresh, entries: fresh.entries.filter((e) => e.id !== id) }));
          throw new Error(saved.reason ?? "Could not create empty workspace.");
        }
        setCatalog(next);
      }
      const url = workspaceUrl(id);
      if (native) await openWorkspaceWindowNative(id);
      else if (placeholder) placeholder.location.href = url;
      else setError(`Popup blocked — open ${url} manually.`);
      setName("");
    } catch (e) { placeholder?.close(); setError(e instanceof Error ? e.message : "Could not create window."); }
  };
  const rename = async (id: string, old: string) => {
    if (id === MONITORING_WORKSPACE_ID) return;
    const value = window.prompt("Workspace name", old); if (value == null) return;
    try {
      if (commands.workspace) {
        const renamed = await commands.workspace.rename({ workspaceId: id, name: validateName(value, catalog.entries.filter((entry) => entry.id !== id).map((entry) => entry.name), ["main", MONITORING_WORKSPACE_NAME]), expectedCatalogRevision: catalog.revision ?? 0 });
        if (renamed.status !== "accepted") throw new Error(renamed.reason ?? "Could not rename workspace.");
        setCatalog(await readWindows(commands));
      } else {
        setCatalog(await mutateWindows(commands, (fresh) => ({ ...fresh, entries: fresh.entries.map((e) => e.id === id ? { ...e, name: validateName(value, fresh.entries.filter((x) => x.id !== id).map((x) => x.name), ["main", MONITORING_WORKSPACE_NAME]) } : e) })));
      }
    } catch (e) { setError(String(e)); }
  };
  const remove = async (id: string) => {
    if (id === MONITORING_WORKSPACE_ID || id === currentId || !window.confirm("Delete this workspace and its saved layout?")) return;
    try {
      if (commands.workspace) {
        const deleted = await commands.workspace.remove({ workspaceId: id, expectedCatalogRevision: catalog.revision ?? 0 });
        if (deleted.status !== "accepted") throw new Error(deleted.reason ?? "Could not delete workspace.");
        setCatalog(await readWindows(commands));
      } else {
        const deleted = await commands.sendCommand("DeleteConfig", { key: `workspace.${id}` });
        if (deleted.status !== "accepted") throw new Error(deleted.reason ?? "Could not delete workspace.");
        setCatalog(await mutateWindows(commands, (fresh) => ({ ...fresh, entries: fresh.entries.filter((e) => e.id !== id) })));
      }
    } catch (e) { setError(e instanceof Error ? e.message : String(e)); }
  };
  return <div onClick={onClose} style={{ position: "fixed", inset: 0, zIndex: 10001, background: "rgba(0,0,0,.5)", display: "grid", placeItems: "center" }}><div onClick={(e) => e.stopPropagation()} style={{ width: 460, padding: 18, background: palette.surface }}>
    <h3>New window</h3>{entries.sort((a,b)=>a.name.localeCompare(b.name)).map((e)=><div key={e.id} style={{display:"flex",gap:8,margin:6}}><Button onClick={()=>openWorkspaceWindow(e.id)}>{e.name}</Button>{e.id !== MONITORING_WORKSPACE_ID && <><Button onClick={()=>void rename(e.id,e.name)}>Rename</Button><Button disabled={e.id===currentId} onClick={()=>void remove(e.id)}>Delete</Button></>}</div>)}
    <div style={{display:"flex",gap:8,marginTop:16}}><input aria-label="Workspace name" value={name} onChange={(e)=>setName(e.target.value)} maxLength={64}/><Button onClick={()=>void create()}>Create new</Button></div>{error&&<p role="alert">{error}</p>}
  </div></div>;
}
