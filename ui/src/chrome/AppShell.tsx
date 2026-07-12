import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { DockviewReact, themeDark, themeLight, type DockviewApi, type DockviewReadyEvent, type IDockviewPanelProps } from "dockview";
// dockview's stylesheet is imported in main.tsx (ahead of global.css) so our
// theme overrides always win the cascade — see the comment there.
import { PanelFrame } from "./PanelFrame";
import type { PanelConfig, Workspace } from "./workspace";
import { WorkspaceStore } from "./workspace";
import type { Stores } from "../data/registry";
import type { Scheduler } from "../render/Scheduler";
import type { LinkGroup, LinkGroups } from "./linkGroups";
import type { DemandRegistry } from "../wire/DemandRegistry";
import type { ConnState } from "../wire/WsClient";
import { PANELS, type PanelProps } from "./panels/registry";
import { PRESETS } from "./presets";
import { TopBar } from "./TopBar";
import { FeedStatusBanner } from "./FeedStatusBanner";
import { BootStatusBanner } from "./BootStatusBanner";
import { ReplayBanner } from "./ReplayBanner";
import { DemoBanner } from "./DemoBanner";
import { AlpacaBackfillBanner } from "./AlpacaBackfillBanner";
import { EmptyState } from "./EmptyState";
import { Catalog } from "./Catalog";
import { parseImport, prepareImportedWorkspace, isPresentLayout } from "./backup";
import { SettingsModal, type SettingsSection } from "./SettingsModal";
import { PracticeLauncherModal } from "./PracticeLauncherModal";
import { VenueSetupPrompt } from "./VenueSetupPrompt";
import { OpenSettingsProvider } from "./OpenSettingsContext";
import { modalTracker } from "./modalTracker";
import { useTheme } from "./ThemeProvider";
import { useToasts } from "./Toast";
import { useOrderCommands } from "./exec/useOrderCommands";
import { useReplayCommands } from "./exec/useReplayCommands";
import { useHotkeys } from "./exec/useHotkeys";
import { useSoundWiring } from "../sound/useSoundWiring";
import { nextWindowName } from "./windows";

// Task 3: permanent "don't show again" flag for the first-run venue-setup
// prompt, set only when the user ticks the checkbox on either action.
const VENUE_SETUP_HIDDEN_KEY = "etape.venueSetupHidden";
function readVenueSetupHidden(): boolean {
  try {
    return localStorage.getItem(VENUE_SETUP_HIDDEN_KEY) === "1";
  } catch {
    return false; // a blocked/unavailable localStorage shouldn't suppress the prompt
  }
}

// Permanent "don't show again" flag for the Alpaca-1m-history hint banner,
// set only when the user clicks its dismiss (✕) button. Separate key from
// the venue-setup prompt above — the two are mutually exclusive (this only
// shows once at least one non-Alpaca venue exists) but independently silenced.
const ALPACA_HINT_HIDDEN_KEY = "etape.alpacaHintHidden";
function readAlpacaHintHidden(): boolean {
  try {
    return localStorage.getItem(ALPACA_HINT_HIDDEN_KEY) === "1";
  } catch {
    return false; // a blocked/unavailable localStorage shouldn't suppress the hint
  }
}

interface Props {
  workspaceName: string;
  stores: Stores;
  scheduler: Scheduler;
  workspaceStore: WorkspaceStore;
  linkGroups: LinkGroups;
  demandRegistry: DemandRegistry;
  commands: PanelProps["commands"];
  engineState: ConnState;
}

export function AppShell({ workspaceName, stores, scheduler, workspaceStore, linkGroups, demandRegistry, commands, engineState }: Props): JSX.Element {
  const [ws, setWs] = useState<Workspace | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  // Unified Settings modal (Task 11): AppShell owns open/section state; TopBar's
  // gear opens it to Appearance, the order ticket's gear (via OpenSettingsContext)
  // opens it straight to Orders & hotkeys.
  const [settings, setSettings] = useState<{ open: boolean; section: SettingsSection }>({ open: false, section: "appearance" });
  // Task 9 (unified into the Task 5/U3 Practice launcher): opened from
  // TopBar's "Practice" button, offers a synthetic demo market or replaying
  // a recorded day.
  const [practiceOpen, setPracticeOpen] = useState(false);
  // Task 3 (venues/creds redesign): first-run venue-setup prompt. Separate from
  // the `etape.venueSetupHidden` localStorage flag below — this only silences
  // the prompt for the REST OF THIS SESSION after either action, so it doesn't
  // re-flash on every re-render while venues are still empty; the localStorage
  // flag (only set when "don't show again" is ticked) is what survives reload.
  const [venueSetupSessionDismissed, setVenueSetupSessionDismissed] = useState(false);
  // Alpaca-1m-history hint banner: session-only dismiss, mirroring the
  // venue-setup prompt's pattern above (see readAlpacaHintHidden for the
  // permanent flag).
  const [alpacaHintSessionDismissed, setAlpacaHintSessionDismissed] = useState(false);
  const { mode } = useTheme();
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  const rc = useReplayCommands(commands);
  // Shared by both <ReplayBanner> and <DemoBanner>'s "Return to live" button —
  // GoLive is the same engine command regardless of which practice mode
  // (replay or demo) is currently active.
  const onGoLive = async () => {
    const ack = await rc.goLive();
    if (ack.status !== "accepted") throw new Error(ack.reason || "Return to live rejected");
  };
  // DockviewApi is only available once dockview mounts (i.e. once the workspace
  // has at least one panel — see the empty-state switch below); null otherwise.
  const apiRef = useRef<DockviewApi | null>(null);
  // Dockview mutations that need a NEW panel id already present in dockview's
  // `components` map (api.addPanel, applyWorkspace's api.fromJSON) must not run
  // synchronously in the same tick as the setWs() that adds the id — dockview's
  // React binding only refreshes its internal `createComponent` closure in a
  // child useEffect that fires after commit, so an immediate call resolves
  // against the STALE map and throws ("Only React.memo/ForwardRef/functional
  // components are accepted"). Queue the mutation here; the flush effect below
  // runs after every ws-driven re-render, which (children-before-parent effect
  // ordering) is always after DockviewReact's own components-sync effect.
  const pendingRef = useRef<Array<(api: DockviewApi) => void>>([]);
  // Lets handlers registered once (onDidRemovePanel, below) read the latest
  // workspace without capturing a stale closure over `ws`.
  const wsRef = useRef<Workspace | null>(null);
  wsRef.current = ws;
  useEffect(() => {
    void workspaceStore.load(workspaceName).then((w) => {
      // Hydrate LinkGroups' per-group focused symbol BEFORE setWs: panels read
      // linkGroups.symbolFor(group) on their very first mount, and mounting
      // starts as soon as `ws` goes non-null below (Bug 5 — a grouped panel's
      // symbol otherwise falls back to its AAPL creation-time seed on refresh,
      // because LinkGroups itself is rebuilt empty on every page load).
      linkGroups.hydrate(w.groups ?? {});
      linkGroups.hydrateVenues(w.linkVenues ?? {});
      setWs(w);
    });
  }, [workspaceName, workspaceStore, linkGroups]);
  // Mounted once, globally — must run unconditionally, before the loading-state
  // early return below, per the Rules of Hooks. group: "blue" (not useHotkeys'
  // own "green" default) — found via the Task 12 E2E smoke spec: no preset in
  // presets.ts ever puts an order ticket/DOM/tape in a "green" group (Monitoring
  // has no execution surface at all; Trading's t-ticket/t-dom/t-tape/charts are
  // all "blue"), so the unqualified default silently resolved to an empty
  // symbol/venue and every place-order hotkey (Ctrl+1..4) no-opped with a "no
  // venue/quote for hotkey" toast in the shipped Trading preset.
  useHotkeys({ stores, commands, linkGroups, group: "blue" });
  useSoundWiring(stores);
  // Task 13: mirror Settings-modal open/close into the module-level modalTracker
  // singleton so every already-mounted PanelFrame (frozen-closure-created, can't
  // receive this as a live prop — see modalTracker.ts) can suppress type-to-load
  // capture while the modal has focus.
  useEffect(() => { modalTracker.setOpen(settings.open); }, [settings.open]);
  // Same exec/armed pattern as AccountBarPanel: subscribe to the exec store so
  // the top bar's arm chip re-renders on masterArmed flips from any source
  // (this bar, the account panel, or the engine).
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const execStatus = stores.exec.status();
  const armed = execStatus?.masterArmed ?? false;
  // A paper "sim" venue is auto-seeded on first run (engine-side config seed),
  // so "no venues configured" is no longer the right signal for either nudge
  // below — a fresh install already has one. Both are re-keyed off "no REAL
  // (non-sim) broker venue" instead, so a new user is still nudged toward
  // live trading until they add TradeZero/Alpaca/moomoo.
  const hasRealVenue = execStatus?.venues.some((v) => v.broker !== "sim") ?? false;
  const sessionMode = useSyncExternalStore((cb) => stores.session.subscribe(cb), () => stores.session.getSnapshot());
  // Task 3: show the first-run venue-setup prompt once the first exec.status
  // snapshot has arrived (execStatus !== null — gates the connect-window flash)
  // and only while no real broker venue is configured, the user hasn't
  // dismissed it THIS session, and hasn't permanently silenced it via the
  // checkbox. Also suppressed during a confirmed replay/demo session
  // (sessionMode.mode === "replay" or "demo") — nudging toward configuring a
  // broker "to trade live" makes no sense mid-replay/demo, and venue edits
  // need an engine restart anyway, which would kill the session. "pending"
  // (mode unconfirmed yet) intentionally still allows it through, same as the
  // prior unconditional "live" default — this only needs to suppress the
  // cases we're SURE are practice sessions.
  const showVenueSetup = execStatus !== null && !hasRealVenue && sessionMode.mode !== "replay"
    && sessionMode.mode !== "demo" && !venueSetupSessionDismissed && !readVenueSetupHidden();
  const dismissVenueSetup = (dontShowAgain: boolean) => {
    if (dontShowAgain) {
      try { localStorage.setItem(VENUE_SETUP_HIDDEN_KEY, "1"); } catch { /* best-effort only */ }
    }
    setVenueSetupSessionDismissed(true);
  };
  const configureVenueSetup = (dontShowAgain: boolean) => {
    dismissVenueSetup(dontShowAgain);
    setSettings({ open: true, section: "venues" });
  };
  // Task 6 (U4): the single "Try demo" entry point shared by both first-run
  // surfaces below (EmptyState + VenueSetupPrompt) — each is a dumb,
  // controlled component that only ever calls this prop, same as their
  // existing onAddPanel/onConfigure/onDismiss callbacks. No dismiss/settings
  // bookkeeping bundled in here (unlike configureVenueSetup above): once
  // StartDemo is accepted, sessionMode.mode flips to "demo" and both
  // surfaces' own gating (showTryDemo below; VenueSetupPrompt's showVenueSetup
  // gate above) hides them naturally. Still surfaces a rejection/transport
  // failure as a toast so a failed StartDemo doesn't fail silently — mirrors
  // the ack-status check in PracticeLauncherModal's onStartDemo, minus the
  // inline pending/error UI that dedicated modal has room for.
  const onTryDemo = () => {
    rc.startDemo().then((ack) => {
      if (ack.status !== "accepted") toast.push({ level: "danger", text: `Try demo: ${ack.reason || "rejected"}` });
    }).catch((err: unknown) => {
      toast.push({ level: "danger", text: `Try demo failed: ${err instanceof Error ? err.message : "unknown error"}` });
    });
  };
  // Gates EmptyState's CTA: hidden once already inside a confirmed demo or
  // replay session (offering "Try demo" while already IN demo mode would be
  // confusing) — "pending" (mode unconfirmed yet) still allows it through,
  // same as the prior unconditional "live" default and showVenueSetup's
  // "pending" treatment above. VenueSetupPrompt doesn't need an equivalent
  // gate: it's already suppressed during replay/demo by showVenueSetup itself.
  const showTryDemo = sessionMode.mode === "live" || sessionMode.mode === "pending";
  // Alpaca-1m-history hint: shown once at least one REAL broker venue is
  // configured (so it never doubles up with the venue-setup prompt above,
  // which covers the sim-only/no-venue case) but none of them is Alpaca — the
  // deep-1m backfill chain then falls back to moomoo's quota-guarded history
  // fetch instead of the quota-free Alpaca SIP path (see
  // AlpacaBackfillBanner.tsx for the detail).
  const hasAlpaca = execStatus?.venues.some((v) => v.broker === "alpaca") ?? false;
  const showAlpacaHint = engineState === "open" && execStatus !== null
    && hasRealVenue && !hasAlpaca
    && !alpacaHintSessionDismissed && !readAlpacaHintHidden();
  const openAlpacaSetup = () => {
    // Session-dismiss only, not the permanent flag — venue edits only apply
    // on the engine's next boot (see VenuesSection's restart banner), so
    // hasAlpaca won't flip until then; session-dismiss just stops the nag
    // for the rest of this run instead of falsely marking it "handled".
    setAlpacaHintSessionDismissed(true);
    setSettings({ open: true, section: "venues" });
  };
  const dismissAlpacaHint = () => {
    try { localStorage.setItem(ALPACA_HINT_HIDDEN_KEY, "1"); } catch { /* best-effort only */ }
    setAlpacaHintSessionDismissed(true);
  };
  // Flush any dockview mutations queued by addPanel/applyPresetToWorkspace once
  // dockview's components map has caught up with the latest `ws`.
  useEffect(() => {
    const api = apiRef.current;
    if (!api || pendingRef.current.length === 0) return;
    const actions = pendingRef.current;
    pendingRef.current = [];
    actions.forEach((fn) => fn(api));
  }, [ws]);
  // The dockview instance is unmounted (see the empty-state switch below) once
  // the last panel is removed; drop the now-disposed api reference so nothing
  // tries to call into it afterward.
  useEffect(() => {
    if (!ws || ws.panels.length === 0) apiRef.current = null;
  }, [ws]);
  // Persist the per-group focused symbol into the workspace doc on every
  // change (Bug 5 — see the load effect above for why LinkGroups itself can't
  // survive a refresh on its own). Must call setWs, not just mutate wsRef:
  // wsRef.current is unconditionally overwritten with `ws` on every render
  // (the assignment right after wsRef's declaration above), so a write that
  // only touched wsRef would be silently reverted by the very next render
  // before it ever reached React state — and every OTHER wsRef-based saver
  // (onConfigChange/onGroupChange/onDidLayoutChange below) spreads
  // wsRef.current, so once `groups` lives in `ws` state those saves preserve
  // it automatically.
  useEffect(() => {
    return linkGroups.subscribe(() => {
      const current = wsRef.current;
      if (!current) return; // a cross-window bus echo can arrive before the first load resolves
      const next = { ...current, groups: linkGroups.snapshot(), linkVenues: linkGroups.snapshotVenues() };
      wsRef.current = next;
      setWs(next);
      workspaceStore.save(next);
    });
  }, [linkGroups, workspaceStore]);
  if (!ws) return <div style={{ padding: 12 }}>loading workspace…</div>;

  // A stable per-panel onConfigChange MERGES a settings patch into
  // ws.panels[i].settings then saves. Merge, not replace: the panels below and
  // PanelFrame all hold config captured once by dockview at panel-creation time,
  // so a caller that re-sent full settings could only rebuild them from that
  // frozen snapshot — a type-to-load symbol commit used to wipe every setting
  // persisted since mount that way (indicators, timeframe, chart settings).
  // Callers therefore send only the keys they're changing.
  // Reads/writes via wsRef (like onGroupChange/removePanel below) rather than the
  // `ws` closed over at render time: the per-panel PanelFrame factory is captured
  // ONCE by dockview at panel-creation time and never re-invoked with a fresh
  // closure later, so a panel created before a subsequent panel was added would
  // otherwise persist a `ws` missing that later panel — silently dropping it from
  // both React state and the saved workspace doc (Finding 1, final-branch review).
  const onConfigChange = (panelId: string, patch: Record<string, unknown>) => {
    const current = wsRef.current ?? ws;
    const next = { ...current, panels: current.panels.map((p) => (p.id === panelId ? { ...p, settings: { ...p.settings, ...patch } } : p)) };
    wsRef.current = next;
    setWs(next);                 // keep local state authoritative for subsequent edits
    workspaceStore.save(next);   // debounced persist (config key workspace.<name>)
  };

  // Re-links (or pins) a panel: PanelFrame's swatch/GroupPicker calls this on a
  // pick. Separate from onConfigChange (which only ever replaces `settings`) since
  // `group` is a sibling field on the same PanelConfig entry, not part of settings.
  // Reads/writes via wsRef (like removePanel) rather than the `ws` closed over at
  // render time: the per-panel PanelFrame factory below is captured ONCE by
  // dockview at panel-creation time and never re-invoked with fresh closures on
  // later AppShell renders (dockview keeps panel content mounted for its whole
  // life so canvas surfaces don't remount on focus/drag) — so this handler must
  // stay correct no matter how stale the `ws` it was originally created against is.
  const onGroupChange = (panelId: string, group: LinkGroup) => {
    const current = wsRef.current ?? ws;
    const next = { ...current, panels: current.panels.map((p) => (p.id === panelId ? { ...p, group } : p)) };
    wsRef.current = next;
    setWs(next);
    workspaceStore.save(next);
  };

  // Allocate a fresh panel id and default settings per panel type, add it to
  // the workspace doc, then (if dockview is already mounted) queue the actual
  // dockview.addPanel call — see the pendingRef comment above for why this
  // can't run synchronously. If the workspace was empty, dockview isn't
  // mounted yet: it mounts fresh on the next render and its onReady seeds the
  // grid directly from the now-updated ws.panels, so no queued action is needed.
  const addPanel = (panelId: string) => {
    const def = PANELS[panelId];
    if (!def) return;
    const id = `${panelId}-${crypto.randomUUID().slice(0, 8)}`;
    const settings: Record<string, unknown> = panelId === "chart" ? { symbol: "US.AAPL", timeframe: "1m" } : {};
    const config: PanelConfig = { id, panelId, group: null, settings };
    const next = { ...ws, panels: [...ws.panels, config] };
    setWs(next);
    workspaceStore.save(next);
    if (apiRef.current) {
      pendingRef.current.push((api) => {
        if (!api.getPanel(id)) api.addPanel({ id, component: id, title: def.title });
      });
    }
    setAddOpen(false);
  };

  // Drop a panel from the workspace doc and close it in dockview (if open).
  // Reads/writes via wsRef so this stays correct whether called from a
  // freshly-rendered handler or from the once-registered onDidRemovePanel
  // listener below (which keeps ws.panels in sync when the user closes a
  // dockview tab directly).
  const removePanel = (id: string) => {
    const current = wsRef.current;
    if (!current || !current.panels.some((p) => p.id === id)) return; // already synced
    const next = { ...current, panels: current.panels.filter((p) => p.id !== id) };
    wsRef.current = next;
    setWs(next);
    workspaceStore.save(next);
    apiRef.current?.getPanel(id)?.api.close();
  };

  // Replace the whole workspace doc with `next` (a preset's panels+layout, or
  // an imported workspace) and re-render dockview to match. Confirms first
  // if `opts.confirm` is given (omit it when the caller already confirmed —
  // e.g. BackupSection's own window.confirm before onImportWorkspace — so we
  // don't double-prompt). Hydrates LinkGroups from `next` BEFORE setWs, same
  // ordering as the load effect above and for the same reason: panels read
  // linkGroups.symbolFor(group) on their very first mount, so a group whose
  // focused symbol isn't hydrated yet would fall back to its AAPL
  // creation-time seed. Same pendingRef deferral as addPanel: if dockview is
  // already mounted, api.fromJSON needs the new panel ids present in the
  // components map first.
  const applyWorkspace = (next: Workspace, opts?: { confirm?: string }) => {
    if (opts?.confirm && !window.confirm(opts.confirm)) return;
    linkGroups.hydrate(next.groups ?? {});
    linkGroups.hydrateVenues(next.linkVenues ?? {});
    setWs(next);
    wsRef.current = next;
    workspaceStore.save(next);
    if (apiRef.current) {
      pendingRef.current.push((api) => {
        api.clear();
        api.fromJSON(next.layout as Parameters<typeof api.fromJSON>[0]);
      });
    }
  };

  // Replace the workspace with a preset's panels + layout. Confirms first if
  // the workspace isn't already empty. The `wsRef.current === next` check
  // below (reference, not deep, equality — `next` is a fresh object every
  // call) is how we tell whether applyWorkspace actually applied `next` vs.
  // bailed out on a cancelled confirm, so the "Add panel" popover only
  // closes on an actual replace, same as before this was extracted.
  const applyPresetToWorkspace = (presetId: string) => {
    const preset = PRESETS.find((p) => p.id === presetId);
    if (!preset) return;
    const current = wsRef.current ?? ws;
    const { panels, layout } = preset.build();
    const next = { ...current, panels, layout };
    applyWorkspace(next, current.panels.length > 0 ? { confirm: "Replace the current layout with this preset?" } : undefined);
    if (wsRef.current === next) setAddOpen(false);
  };

  // Import & export (Task 3): BackupSection already ran its own
  // window.confirm before calling this, so no confirm string here — a
  // second confirm would double-prompt the user.
  const onImportWorkspace = (w: Workspace) => applyWorkspace(w);

  // Empty-workspace "Import layout" entry point: same parseImport/
  // prepareImportedWorkspace/applyWorkspace pipeline as BackupSection, but
  // layout-only (ignores hotkeys even if the file has them, matching the
  // label) and no confirm — the empty state has nothing to lose.
  const importLayoutFile = (file: File) => {
    const reader = new FileReader();
    reader.onload = () => {
      const text = typeof reader.result === "string" ? reader.result : "";
      const result = parseImport(text);
      if (!result.ok) { toast.push({ level: "danger", text: result.error }); return; }
      if (!isPresentLayout(result.data.layout)) {
        toast.push({ level: "danger", text: "That file has no layout to import." });
        return;
      }
      applyWorkspace(prepareImportedWorkspace(result.data.layout, (wsRef.current ?? ws).name));
      toast.push({ level: "info", text: "Imported layout." });
    };
    reader.readAsText(file);
  };

  // "Connection" in the latency readout: focus the existing connection-status
  // panel if the workspace already has one, otherwise add it.
  const onOpenConnection = () => {
    const current = wsRef.current ?? ws;
    const existing = current.panels.find((p) => p.panelId === "connection-status");
    if (existing) apiRef.current?.getPanel(existing.id)?.focus();
    else addPanel("connection-status");
  };

  // Best-effort window tracking in localStorage so repeated "New window"
  // clicks fill the lowest free window-N gap rather than colliding.
  const onNewWindow = () => {
    let known: string[] = [];
    try {
      known = JSON.parse(localStorage.getItem("etape.windows") ?? "[]") as string[];
    } catch {
      known = [];
    }
    const names = Array.from(new Set([workspaceName, ...known]));
    const name = nextWindowName(names);
    try {
      localStorage.setItem("etape.windows", JSON.stringify([...names, name]));
    } catch {
      // best-effort only — a full/blocked localStorage shouldn't stop the new window
    }
    const url = `?workspace=${name}`;
    const w = window.open(url, "_blank");
    if (!w) toast.push({ level: "warn", text: `Popup blocked — open ${url} manually.`, sticky: true });
  };

  // Stable React keys: panels are keyed by config.id so dockview drag/resize
  // never remounts them (canvas keeps its context). Each factory is called by
  // dockview exactly ONCE, at panel-creation time, and the resulting element is
  // kept mounted (portal'd) for the panel's whole life — dockview does supply
  // fresh `api`/`containerApi`/`params` props on every re-render of that portal
  // (see IDockviewPanelProps), so PanelFrame reads its own liveness via
  // `props.api` (a stable, subscribable object) rather than a boolean baked
  // into this closure at creation time.
  const components = Object.fromEntries(
    ws.panels.map((p) => [
      p.id,
      (panelProps: IDockviewPanelProps) => <PanelFrame config={p} stores={stores} scheduler={scheduler}
        linkGroups={linkGroups} demandRegistry={demandRegistry} commands={commands}
        onConfigChange={(settings) => onConfigChange(p.id, settings)}
        onGroupChange={(group) => onGroupChange(p.id, group)}
        onClose={() => removePanel(p.id)}
        api={panelProps.api} />,
    ]),
  );

  // A pane's tab strip is only useful once there's more than one panel to switch
  // between — a lone panel's own header (PanelFrame's ledger-header) already shows
  // its title, so the dockview tab above it is pure redundant chrome.
  const syncTabVisibility = (api: DockviewApi) => {
    for (const g of api.groups) g.header.hidden = g.panels.length <= 1;
  };

  const onReady = (event: DockviewReadyEvent) => {
    apiRef.current = event.api;
    // Restore a previously saved dockview layout if present; otherwise seed the grid
    // from the panel list (first run — the seed's `layout` is a placeholder string).
    let restored = false;
    const layout = ws.layout as { grid?: unknown } | null;
    try {
      if (layout && typeof layout.grid === "object" && layout.grid !== null) {
        event.api.fromJSON(layout as Parameters<typeof event.api.fromJSON>[0]);
        restored = true;
      }
    } catch {
      restored = false;
    }
    if (!restored) {
      ws.panels.forEach((p, i) => {
        event.api.addPanel({
          id: p.id, component: p.id, title: p.panelId,
          ...(i === 0 ? {} : { position: { direction: i % 2 ? "right" : "below" } as const }),
        });
      });
    }
    syncTabVisibility(event.api);
    // Keep ws.panels in sync when the user closes a dockview tab directly
    // (previously only the layout was re-saved on removal, leaving the closed
    // panel's config as a zombie entry in the workspace doc).
    event.api.onDidRemovePanel((panel) => removePanel(panel.id));
    event.api.onDidLayoutChange(() => {
      // Read via wsRef, not the `ws` this closure was created with: addPanel /
      // removePanel / applyPresetToWorkspace can change ws.panels after this
      // mount-time closure was captured, and onDidLayoutChange fires on every
      // drag/resize thereafter — saving the stale `ws` here would silently
      // drop any panel added/removed since mount from the persisted doc.
      const current = wsRef.current ?? ws;
      workspaceStore.save({ ...current, layout: event.api.toJSON() });
      // "an aggregation of many events" (dockview's own doc comment) — covers
      // every panel/group add, remove, and move, so a group's tab strip stays
      // in sync with its current panel count after any rearrangement.
      syncTabVisibility(event.api);
    });
  };

  return (
    <OpenSettingsProvider value={{ openOrderSettings: () => setSettings({ open: true, section: "orders" }) }}>
      <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
        <div style={{ position: "relative" }}>
          <TopBar workspaceName={workspaceName} health={stores.health} armed={armed}
            onArmToggle={() => (armed ? oc.disarm() : oc.arm())}
            onAddPanel={() => setAddOpen((v) => !v)}
            onNewWindow={onNewWindow}
            onOpenSettings={() => setSettings({ open: true, section: "appearance" })}
            onOpenConnection={onOpenConnection}
            onOpenReplay={() => setPracticeOpen(true)}
          />
          {addOpen && (
            <div className="popover" style={{ top: 40, right: 160, width: 580, maxHeight: "70vh", overflow: "auto" }}>
              <Catalog onAddPanel={addPanel} onApplyPreset={applyPresetToWorkspace} />
            </div>
          )}
        </div>
        <BootStatusBanner boot={stores.boot} />
        <ReplayBanner session={stores.session} engineState={engineState} onGoLive={onGoLive} />
        <DemoBanner session={stores.session} engineState={engineState} onGoLive={onGoLive} />
        <FeedStatusBanner health={stores.health} boot={stores.boot} engineState={engineState} onOpenConnection={onOpenConnection} />
        {showAlpacaHint && <AlpacaBackfillBanner onSetup={openAlpacaSetup} onDismiss={dismissAlpacaHint} />}
        <div style={{ flex: 1, minHeight: 0 }}>
          {ws.panels.length === 0 ? (
            <EmptyState onAddPanel={addPanel} onApplyPreset={applyPresetToWorkspace} showTryDemo={showTryDemo} onTryDemo={onTryDemo} onImportLayoutFile={importLayoutFile} />
          ) : (
            <DockviewReact components={components} onReady={onReady}
              theme={mode === "light" ? themeLight : themeDark} />
          )}
        </div>
        <SettingsModal open={settings.open} section={settings.section}
          onSection={(s) => setSettings((v) => ({ ...v, section: s }))}
          onClose={() => setSettings((v) => ({ ...v, open: false }))}
          commands={commands}
          getWorkspace={() => wsRef.current ?? ws}
          onImportWorkspace={onImportWorkspace}
          toast={toast}
          engineState={engineState} />
        <PracticeLauncherModal open={practiceOpen} onClose={() => setPracticeOpen(false)} commands={commands} />
        {showVenueSetup && <VenueSetupPrompt onConfigure={configureVenueSetup} onDismiss={dismissVenueSetup} onTryDemo={onTryDemo} />}
      </div>
    </OpenSettingsProvider>
  );
}
