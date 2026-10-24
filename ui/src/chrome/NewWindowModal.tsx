import { useEffect, useState } from "react";
import { Button } from "./controls/Button";
import { mutateWindows, readWindows, validateName, type CommandClient, type WindowCatalogV1 } from "./catalogs";
import { Dialog } from "./controls/Dialog";
import { blankWorkspace, MONITORING_WORKSPACE_ID, MONITORING_WORKSPACE_NAME, type WorkspaceStore } from "./workspace";
import { openWorkspaceWindow, workspaceUrl, workspaceWindowFeatures } from "./windows";

export function NewWindowModal({ open, currentId, commands, workspaceStore, onClose }: { open: boolean; currentId: string; commands: CommandClient; workspaceStore?: WorkspaceStore; onClose: () => void }): JSX.Element | null {
  const [catalog, setCatalog] = useState<WindowCatalogV1>({ version: 1, entries: [] });
  const [name, setName] = useState(""); const [error, setError] = useState("");
  const [editing, setEditing] = useState<{ id: string; name: string } | null>(null);
  useEffect(() => { if (open) void readWindows(commands).then(setCatalog); }, [open, commands]);
  useEffect(() => { if (open) { setName(""); setError(""); setEditing(null); } }, [open]);
  if (!open) return null;
  const entries = [
    { id: MONITORING_WORKSPACE_ID, name: MONITORING_WORKSPACE_NAME },
    ...catalog.entries.filter((entry) => entry.id !== MONITORING_WORKSPACE_ID),
  ];
  const select = (id: string) => { if (openWorkspaceWindow(id)) onClose(); };
  const create = async () => {
    setError(""); const placeholder = window.open("about:blank", "_blank", workspaceWindowFeatures());
    try {
      const clean = validateName(name, catalog.entries.map((e) => e.name), ["main", MONITORING_WORKSPACE_NAME]); const id = crypto.randomUUID();
      const next = await mutateWindows(commands, (fresh) => ({ ...fresh, entries: [...fresh.entries, { id, name: validateName(clean, fresh.entries.map((e) => e.name), ["main", MONITORING_WORKSPACE_NAME]) }] }));
      const saved = await commands.sendCommand("SetConfig", { key: `workspace.${id}`, value: blankWorkspace(id) });
      if (saved.status !== "accepted") {
        await mutateWindows(commands, (fresh) => ({ ...fresh, entries: fresh.entries.filter((e) => e.id !== id) }));
        throw new Error(saved.reason ?? "Could not create empty workspace.");
      }
      setCatalog(next); const url = workspaceUrl(id);
      if (placeholder) { placeholder.location.href = url; onClose(); } else setError(`Popup blocked — open ${url} manually.`);
      setName("");
    } catch (e) { placeholder?.close(); setError(e instanceof Error ? e.message : "Could not create window."); }
  };
  const rename = async (id: string, value: string) => {
    if (id === MONITORING_WORKSPACE_ID) return;
    try {
      setCatalog(await mutateWindows(commands, (fresh) => ({ ...fresh, entries: fresh.entries.map((e) => e.id === id ? { ...e, name: validateName(value, fresh.entries.filter((x) => x.id !== id).map((x) => x.name), ["main", MONITORING_WORKSPACE_NAME]) } : e) })));
      setEditing(null); setError("");
    } catch (e) { setError(String(e)); }
  };
  const remove = async (id: string) => {
    if (id === MONITORING_WORKSPACE_ID || !navigator.locks || id === currentId) return;
    await navigator.locks.request(`etape.workspace.${id}`, { mode: "exclusive", ifAvailable: true }, async (lock) => {
      if (!lock) { setError("That workspace is open in another tab."); return; }
      const deleted = await commands.sendCommand("DeleteConfig", { key: `workspace.${id}` });
      if (deleted.status === "accepted") workspaceStore?.notify(id);
      setCatalog(await mutateWindows(commands, (fresh) => ({ ...fresh, entries: fresh.entries.filter((e) => e.id !== id) })));
    });
  };
  return <Dialog title="New window" onClose={onClose} width={500}
    footer={<><span className="dialog-hint">Open a saved workspace or create an empty one.</span><Button onClick={onClose}>Done</Button></>}>
    {entries.sort((a,b)=>a.name.localeCompare(b.name)).map((e)=><div key={e.id} className="workspace-row">
      {editing?.id === e.id ? <form style={{ display: "flex", gap: 6, width: "100%" }} onSubmit={(event) => { event.preventDefault(); void rename(e.id, editing.name); }}>
        <input className="field" aria-label="Rename workspace" autoFocus value={editing.name} maxLength={64} style={{ flex: 1, minWidth: 0 }} onChange={(event) => setEditing({ id: e.id, name: event.target.value })}/>
        <Button type="submit" disabled={!editing.name.trim()}>Save</Button><Button onClick={() => setEditing(null)}>Cancel</Button>
      </form> : <><Button onClick={()=>select(e.id)}>{e.name}</Button>{e.id !== MONITORING_WORKSPACE_ID && <>
        <Button variant="quiet" onClick={()=>setEditing({ id: e.id, name: e.name })}>Rename</Button>
        <Button variant="danger" confirm confirmLabel="Confirm delete" disabled={!navigator.locks || e.id===currentId} onClick={()=>void remove(e.id)}>Delete</Button>
      </>}</>}
    </div>)}
    <form style={{display:"flex",gap:8,marginTop:20}} onSubmit={(event) => { event.preventDefault(); void create(); }}>
      <input className="field" aria-label="Workspace name" placeholder="New workspace name" value={name} onChange={(e)=>setName(e.target.value)} maxLength={64} style={{ flex: 1, minWidth: 0 }}/>
      <Button variant="primary" type="submit" disabled={!name.trim()}>Create new</Button>
    </form>{error&&<p role="alert" style={{ color: "var(--danger)" }}>{error}</p>}
  </Dialog>;
}
