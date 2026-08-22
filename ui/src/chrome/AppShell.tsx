import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { DockviewReact, type DockviewApi, type DockviewReadyEvent, type IDockviewPanelProps } from "dockview-react";
import { themeDark, themeLight } from "dockview";
// dockview's stylesheet is imported in main.tsx (ahead of global.css) so our
// theme overrides always win the cascade — see the comment there.
import { PanelFrame } from "./PanelFrame";
import { isCurrentWorkspace, MONITORING_WORKSPACE_ID, MONITORING_WORKSPACE_NAME, type PanelConfig, type ScannerSyncConfig, type Workspace } from "./workspace";
import { WorkspaceStore } from "./workspace";
import type { Stores } from "../data/registry";
import type { Scheduler } from "../render/Scheduler";
import type { LinkGroup, LinkGroups } from "./linkGroups";
import { HotkeyTargetCoordinator, type HotkeyTargetChannel, type HotkeyTargetInput } from "./hotkeyTarget";
import type { DemandRegistry } from "../wire/DemandRegistry";
import type { ConnState } from "../wire/WsClient";
import { PANELS, dockviewPanelConstraints, type PanelProps } from "./panels/registry";
import { buildMonitoringWorkspace, PRESETS } from "./presets";
import { TopBar, targetCueFor } from "./TopBar";
import { FeedStatusBanner } from "./FeedStatusBanner";
import { BootStatusBanner } from "./BootStatusBanner";
import { DemoBanner } from "./DemoBanner";
import { AlpacaBackfillBanner } from "./AlpacaBackfillBanner";
import { EmptyState } from "./EmptyState";
import { Catalog } from "./Catalog";
import { parseImport, prepareImportedWorkspace, isCurrentLayout, isPresentLayout, reconcileToGrid, applyPanelConstraintsToLayout, orderedPanelIds } from "./backup";
import { SettingsModal, type SettingsSection } from "./SettingsModal";
import { PracticeLauncherModal } from "./PracticeLauncherModal";
import { VenueSetupPrompt } from "./VenueSetupPrompt";
import { OpenSettingsProvider } from "./OpenSettingsContext";
import { modalTracker } from "./modalTracker";
import { useTheme } from "./ThemeProvider";
import { useToasts } from "./Toast";
import { useOrderCommands } from "./exec/useOrderCommands";
import { useOrderConfig } from "./exec/useOrderConfig";
import { useHotkeys } from "./exec/useHotkeys";
import { useAutoUnlockOnStartup } from "./exec/useAutoUnlockOnStartup";
import { useSoundWiring } from "../sound/useSoundWiring";
import { NewWindowModal } from "./NewWindowModal";
import { mutateWindows, readWindows } from "./catalogs";
import { isNativeWindow, openWorkspaceWindow, workspaceWindowTarget } from "./windows";
import { planDemoEntry, planDemoRevert } from "./demoTransition";
import { resolveVenue } from "./exec/venueSelection";
import { PanelHeaderTab } from "./PanelHeaderTab";
import { PanelHeaderHostProvider } from "./panels/headerSlot";
import { PanelSymbolRuntime, planScannerSync, rankScannerRows, readScannerSort, ScannerSyncRuntime, type ScannerSyncPanelState, type ScannerSyncPlan } from "./scannerSync";

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

// AppShell only needs these execution fields. Keep this a primitive so
// useSyncExternalStore can retain the same snapshot across account/P&L updates
// and status replacements that do not change shell behavior.
function appShellExecSignature(stores: Stores): string {
  const status = stores.exec.status();
  if (status === null) return "pending";
  const venues = status.venues
    .map((v) => `${v.venue}:${v.broker}`)
    .sort()
    .join(",");
  return `ready|armed=${status.masterArmed ? 1 : 0}|venues=${venues}`;
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
  hotkeyTargetChannel?: HotkeyTargetChannel;
  // Task 13: fired after the demo-mode entry/revert effect below has finished
  // applying a workspace patch for a mode-edge transition — App.tsx wires this
  // to ReannounceGate.onTransitionApplied() so a reconnect that lands on a
  // *different* session mode doesn't re-announce demands until this mode's
  // panel/symbol state is actually in place.
  onTransitionApplied?: () => void;
}

export function AppShell({ workspaceName, stores, scheduler, workspaceStore, linkGroups, demandRegistry, commands, engineState, hotkeyTargetChannel, onTransitionApplied }: Props): JSX.Element {
  const [ws, setWs] = useState<Workspace | null>(null);
  const [sourceWorkspace, setSourceWorkspace] = useState<Workspace | null>(null);
  const [syncConfig, setSyncConfig] = useState<ScannerSyncConfig | undefined>();
  const [addOpen, setAddOpen] = useState(false);
  // Unified Settings modal (Task 11): AppShell owns open/section state; TopBar's
  // gear opens it to Appearance, the order ticket's gear (via OpenSettingsContext)
  // opens it straight to Orders & hotkeys.
  const [settings, setSettings] = useState<{ open: boolean; section: SettingsSection }>({ open: false, section: "general" });
  const [newWindowOpen, setNewWindowOpen] = useState(false);
  const [workspaceLabel, setWorkspaceLabel] = useState(workspaceName);
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
  const orderConfig = useOrderConfig();
  const [windowId] = useState(() => isNativeWindow() ? workspaceWindowTarget(workspaceName) : crypto.randomUUID());
  const hotkeyCoordinator = useMemo(
    () => new HotkeyTargetCoordinator(windowId, hotkeyTargetChannel),
    [hotkeyTargetChannel, windowId],
  );
  const panelSymbols = useMemo(() => new PanelSymbolRuntime(), []);
  const scannerSyncRuntime = useMemo(() => new ScannerSyncRuntime(), []);
  const hotkeyTarget = useSyncExternalStore(
    (cb) => hotkeyCoordinator.subscribe(cb),
    () => hotkeyCoordinator.snapshot(),
    () => null,
  );
  const coordinatorCloseTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
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
  // applyWorkspace's api.clear()+api.fromJSON() dance (below) is a full
  // teardown/rebuild of dockview's OWN panel set used to force already-
  // mounted panels to pick up new settings (dockview panels are otherwise
  // frozen at creation — see PanelFrame's factory comment below). When
  // `next.panels` keeps the SAME ids as before (true for Task 13's demo
  // entry/revert, which only rewrites settings/groups, never adds/removes
  // panels), api.clear() fires onDidRemovePanel for each of those ids before
  // fromJSON re-adds them — indistinguishable, from that listener's point of
  // view, from the user closing every tab. Without this guard, removePanel
  // would drop each one from ws.panels for good, right before fromJSON
  // re-mounts it in dockview with nothing backing it in the workspace doc.
  // Set true for the duration of applyWorkspace's own clear()+fromJSON() call
  // so onDidRemovePanel's handler skips its bookkeeping — applyWorkspace has
  // already established the correct final panel list via its own setWs.
  const applyingWorkspaceRef = useRef(false);
  // Lets handlers registered once (onDidRemovePanel, below) read the latest
  // workspace without capturing a stale closure over `ws`.
  const wsRef = useRef<Workspace | null>(null);
  const syncConfigRef = useRef<ScannerSyncConfig | undefined>(undefined);
  const monitoringWorkspaceRef = useRef<Workspace | null>(null);
  const sourceWorkspaceRef = useRef<Workspace | null>(null);
  wsRef.current = ws;
  sourceWorkspaceRef.current = sourceWorkspace;
  const observeScannerSync = useCallback((next: ScannerSyncConfig | undefined) => {
    syncConfigRef.current = next;
    setSyncConfig(next);
  }, []);
  const persistMonitoringSync = useCallback((nextSync: ScannerSyncConfig) => {
    const save = (current: Workspace) => {
      const next = { ...current, scannerSync: nextSync };
      monitoringWorkspaceRef.current = next;
      if (workspaceName === MONITORING_WORKSPACE_ID) {
        wsRef.current = next;
        setWs(next);
      }
      workspaceStore.save(next);
    };
    if (workspaceName === MONITORING_WORKSPACE_ID) {
      const current = wsRef.current;
      if (current) save(current);
      return;
    }
    const current = monitoringWorkspaceRef.current;
    if (current) save(current);
    else void workspaceStore.load(MONITORING_WORKSPACE_ID).then(save);
  }, [workspaceName, workspaceStore]);
  const updateScannerSync = useCallback((patch: Partial<ScannerSyncConfig>) => {
    const merged = { ...(syncConfigRef.current ?? { enabled: false }), ...patch };
    const next = merged.sourcePanelId && !merged.sourceWorkspaceId
      ? { ...merged, sourceWorkspaceId: MONITORING_WORKSPACE_ID }
      : merged;
    observeScannerSync(next);
    persistMonitoringSync(next);
  }, [observeScannerSync, persistMonitoringSync]);
  const selectScannerSource = useCallback((panelId: string) => {
    updateScannerSync({ enabled: true, sourceWorkspaceId: workspaceName, sourcePanelId: panelId });
  }, [updateScannerSync, workspaceName]);
  const toggleScannerSync = useCallback(() => {
    const current = syncConfigRef.current;
    const sourceId = current?.sourceWorkspaceId ?? MONITORING_WORKSPACE_ID;
    if (!current || !current.sourcePanelId || sourceId !== workspaceName) return;
    updateScannerSync({ enabled: !current.enabled });
  }, [updateScannerSync, workspaceName]);
  useEffect(() => {
    let alive = true;
    setWs(null);
    setSourceWorkspace(null);
    if (workspaceName === MONITORING_WORKSPACE_ID) observeScannerSync(undefined);
    void workspaceStore.load(workspaceName, workspaceName === MONITORING_WORKSPACE_ID ? buildMonitoringWorkspace() : undefined).then((w) => {
      if (!alive) return;
      // Hydrate LinkGroups' per-group focused symbol BEFORE setWs: panels read
      // linkGroups.symbolFor(group) on their very first mount, and mounting
      // starts as soon as `ws` goes non-null below (a grouped panel would
      // otherwise mount without its saved focused symbol because LinkGroups
      // itself is rebuilt empty on every page load).
      linkGroups.hydrate(w.groups ?? {});
      linkGroups.hydrateVenues(w.linkVenues ?? {});
      if (workspaceName === MONITORING_WORKSPACE_ID) {
        monitoringWorkspaceRef.current = w;
        observeScannerSync(w.scannerSync);
      }
      wsRef.current = w;
      setWs(w);
    });
    return () => { alive = false; };
  }, [workspaceName, workspaceStore, linkGroups, observeScannerSync]);
  useEffect(() => {
    let alive = true;
    const refresh = () => void workspaceStore.load(MONITORING_WORKSPACE_ID).then((w) => {
      if (!alive) return;
      monitoringWorkspaceRef.current = w;
      observeScannerSync(w.scannerSync);
      if (workspaceName === MONITORING_WORKSPACE_ID) {
        linkGroups.hydrate(w.groups ?? {});
        linkGroups.hydrateVenues(w.linkVenues ?? {});
        wsRef.current = w;
        setWs(w);
      }
    });
    const unwatch = workspaceStore.watch(MONITORING_WORKSPACE_ID, refresh);
    if (workspaceName !== MONITORING_WORKSPACE_ID) refresh();
    return () => { alive = false; unwatch(); };
  }, [workspaceName, workspaceStore, linkGroups, observeScannerSync]);
  useEffect(() => {
    const sourceId = syncConfig?.sourceWorkspaceId ?? MONITORING_WORKSPACE_ID;
    if (!syncConfig?.sourcePanelId || sourceId === MONITORING_WORKSPACE_ID) {
      setSourceWorkspace(null);
      return;
    }
    let alive = true;
    const refresh = () => void workspaceStore.load(sourceId).then((w) => {
      if (alive) setSourceWorkspace(w);
    });
    const unwatch = workspaceStore.watch(sourceId, refresh);
    refresh();
    return () => { alive = false; unwatch(); };
  }, [syncConfig?.sourcePanelId, syncConfig?.sourceWorkspaceId, workspaceStore]);
  useEffect(() => {
    if (workspaceName === "main") { setWorkspaceLabel("main"); return; }
    if (workspaceName === MONITORING_WORKSPACE_ID) { setWorkspaceLabel(MONITORING_WORKSPACE_NAME); return; }
    const refresh = () => void readWindows(commands).then((c) => setWorkspaceLabel(c.entries.find((e) => e.id === workspaceName)?.name ?? workspaceName));
    refresh(); const channel = new BroadcastChannel("etape.window-catalog"); channel.onmessage = refresh;
    return () => channel.close();
  }, [workspaceName, commands]);
  useEffect(() => {
    if (workspaceName === "main" || !navigator.locks) return;
    const stop = new AbortController();
    void navigator.locks.request(`etape.workspace.${workspaceName}`, { mode: "shared", signal: stop.signal }, () => new Promise<void>((resolve) => stop.signal.addEventListener("abort", () => resolve(), { once: true }))).catch(() => {});
    return () => stop.abort();
  }, [workspaceName]);
  useEffect(() => {
    if (coordinatorCloseTimerRef.current !== null) {
      clearTimeout(coordinatorCloseTimerRef.current);
      coordinatorCloseTimerRef.current = null;
    }
    const clearOwnedTarget = () => {
      const current = hotkeyCoordinator.snapshot();
      if (current?.ownerWindow === windowId) hotkeyCoordinator.clearOwned(current.panel);
    };
    const closeCoordinator = () => hotkeyCoordinator.close();
    window.addEventListener("beforeunload", clearOwnedTarget);
    window.addEventListener("unload", closeCoordinator);
    return () => {
      window.removeEventListener("beforeunload", clearOwnedTarget);
      window.removeEventListener("unload", closeCoordinator);
      // React StrictMode replays effects immediately; defer disposal so its
      // replay can cancel this timer, while a real unmount still closes the
      // ephemeral channel and clears the owner target.
      coordinatorCloseTimerRef.current = setTimeout(() => {
        coordinatorCloseTimerRef.current = null;
        hotkeyCoordinator.close();
      }, 0);
    };
  }, [hotkeyCoordinator, windowId]);
  useEffect(() => {
    void commands.sendCommand("GetConfig", { key: "windows.v1" }).then(async (ack) => {
      if (ack.value !== undefined || localStorage.getItem("etape.windows") == null) return;
      let legacy: string[]; try { legacy = JSON.parse(localStorage.getItem("etape.windows") ?? "[]"); } catch { legacy = []; }
      const names = legacy.filter((n) => typeof n === "string" && n !== "main");
      if (!names.length) { localStorage.removeItem("etape.windows"); return; }
      await mutateWindows(commands, (fresh) => fresh.entries.length ? fresh : ({ version: 1, entries: [...new Set(names)].map((name) => ({ id: name, name })) }));
      localStorage.removeItem("etape.windows");
    }).catch(() => {});
  }, [commands]);
  useSoundWiring(stores);
  // Task 13: mirror Settings-modal open/close into the module-level modalTracker
  // singleton so every already-mounted PanelFrame (frozen-closure-created, can't
  // receive this as a live prop — see modalTracker.ts) can suppress type-to-load
  // capture while the modal has focus.
  useEffect(() => { modalTracker.setOpen(settings.open); }, [settings.open]);
  // Subscribe only to shell-relevant execution state. Account/P&L updates stay
  // in AccountPanel's own subscription and must not re-render Dockview's owner.
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => appShellExecSignature(stores));
  const execStatus = stores.exec.status();
  const armed = execStatus?.masterArmed ?? false;
  const execStatusRef = useRef(execStatus);
  execStatusRef.current = execStatus;
  const activeVenueRef = useRef(orderConfig.config.activeVenue);
  activeVenueRef.current = orderConfig.config.activeVenue;
  const hotkeyTargetInputForPanel = useCallback((panelId: string): HotkeyTargetInput | null => {
    const panel = wsRef.current?.panels.find((p) => p.id === panelId);
    if (!panel) return null;
    const group = panel.group;
    return {
      panel: panel.id,
      group,
      ...(group ? { symbol: linkGroups.symbolFor(group) } : { symbol: panel.settings.symbol as string | undefined }),
      venue: resolveVenue(group, linkGroups, activeVenueRef.current, execStatusRef.current),
    };
  }, [linkGroups]);
  const refreshOwnedTarget = useCallback(() => {
    const current = hotkeyCoordinator.snapshot();
    if (!current || current.ownerWindow !== windowId) return;
    const input = hotkeyTargetInputForPanel(current.panel);
    if (!input) {
      hotkeyCoordinator.clearOwned(current.panel);
      return;
    }
    hotkeyCoordinator.updateOwned(current.panel, input);
  }, [hotkeyCoordinator, hotkeyTargetInputForPanel, windowId]);
  useHotkeys({ stores, commands, target: hotkeyTarget });
  // Auto-unlock-on-startup (fire-once latch — see the hook's own comment):
  // arms trading automatically once the engine connection is up, if the user
  // opted in via Settings → General. `orderConfig` is read here (rather than
  // via `oc`, the order-COMMANDS handle above) because the setting itself
  // lives in the order-CONFIG blob.
  useAutoUnlockOnStartup({
    ready: orderConfig.loaded && execStatus !== null,
    enabled: orderConfig.config.autoUnlockOnStartup ?? false,
    armed,
    onUnlock: useCallback(() => { void oc.arm(); }, [oc]),
  });
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
  const showVenueSetup = execStatus !== null && !hasRealVenue && sessionMode.mode !== "demo"
    && !venueSetupSessionDismissed && !readVenueSetupHidden();
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
    commands.sendCommand("StartDemo", {}).then((ack) => {
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
  // Alpaca-1m-history hint: shown whenever the engine is open and no Alpaca
  // venue is configured — including the sim-only/no-venue case, since that's
  // exactly when the deep-1m backfill chain falls back to moomoo's
  // quota-guarded history fetch instead of the quota-free Alpaca SIP path
  // (see AlpacaBackfillBanner.tsx for the detail). Suppressed while the
  // venue-setup modal is up (it covers first-run and would otherwise double
  // up) and during replay/demo (venue edits need an engine restart, pointless
  // mid-practice) — this is the persistent reminder that takes over once the
  // one-shot modal is dismissed.
  const hasAlpaca = execStatus?.venues.some((v) => v.broker === "alpaca") ?? false;
  const showAlpacaHint = engineState === "open" && execStatus !== null
    && !hasAlpaca
    && sessionMode.mode !== "demo"
    && !showVenueSetup
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
      if (current) {
        const next = { ...current, groups: linkGroups.snapshot(), linkVenues: linkGroups.snapshotVenues() };
        wsRef.current = next;
        setWs(next);
        workspaceStore.save(next);
      }
      refreshOwnedTarget();
    });
  }, [linkGroups, refreshOwnedTarget, workspaceStore]);
  useEffect(() => { refreshOwnedTarget(); }, [refreshOwnedTarget, ws]);
  // Task 13: demo-mode entry/revert orchestration. `wsLoaded` (a derived
  // boolean, not raw `ws`) is the effect's second dependency below,
  // deliberately: it flips false->true exactly once (the workspace doc never
  // goes back to null after its first load), so including it fixes the one
  // real race here — a fresh page load where the very first sys.session
  // snapshot ("pending"->"demo"/"live") beats workspaceStore.load()'s
  // GetConfig round-trip — without making the effect re-run (and tear down an
  // in-flight entry barrier) on every LATER, unrelated setWs call from
  // addPanel/onConfigChange/etc. once ws is already loaded.
  const wsLoaded = ws !== null;
  const prevModeRef = useRef(sessionMode.mode);
  // Pre-demo workspace doc, captured on live/replay->demo and restored
  // verbatim on demo->live. A ref (not a module-level `let`) is enough: it
  // only needs to survive across renders of this ONE mounted AppShell, and a
  // demo relaunch (StartDemo while already live, or GoLive) never remounts
  // this component — the tab never reloads across a demo transition.
  const demoSnapshotRef = useRef<Workspace | null>(null);
  // Belt-and-suspenders re-entrancy guard alongside the effect's own
  // subscribe/timeout cleanup below (which already tears down a pending entry
  // barrier the instant mode flips again, since sessionMode.mode is a dep):
  // bumped at the start of EVERY tracked edge (entry and revert alike), and
  // checked by a pending entry's continuation before it applies anything, so
  // even if the cleanup wiring is ever changed later without noticing this
  // dependency, a superseded continuation still can't slip a stale
  // planDemoEntry patch through.
  const transitionEpochRef = useRef(0);

  useEffect(() => {
    const mode = sessionMode.mode;
    const prev = prevModeRef.current;
    const isEntry = (prev === "live" || prev === "pending") && mode === "demo";
    const isRevert = prev === "demo" && mode === "live";
    if (!isEntry && !isRevert) {
      // demo->demo (e.g. a WS reconnect mid-demo) or any other pair: no-op —
      // critically, this preserves whatever symbols the user set mid-demo.
      prevModeRef.current = mode;
      return;
    }
    // Workspace doc not loaded yet (see the wsLoaded comment above) —
    // deliberately does NOT update prevModeRef.current, so the re-run this
    // effect gets once wsLoaded flips true still sees this same edge.
    const wsNow = wsRef.current ?? ws;
    if (!wsNow) return;

    prevModeRef.current = mode;
    const myEpoch = ++transitionEpochRef.current;

    if (isRevert) {
      // No entry barrier needed on revert — GoLive already implies a real
      // session, so there's no "wait for the watchlist to arrive" step.
      const universe = stores.watchlist.getSnapshot().symbols;
      applyWorkspace(planDemoRevert({ snapshot: demoSnapshotRef.current, universe }, wsRef.current ?? wsNow));
      onTransitionApplied?.();
      return; // synchronous — nothing to await, nothing to clean up
    }

    // Entry edge (live/replay/pending -> demo): snapshot BEFORE anything else,
    // per edge kind — a pending->demo entry (the engine was already in demo
    // when this UI (re)connected) has no real pre-demo doc to snapshot.
    demoSnapshotRef.current = prev === "live" ? structuredClone(wsNow) : null;

    let unsubWatchlist: (() => void) | null = null;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let settled = false;

    const finishEntry = (universe: string[]) => {
      if (settled) return;
      settled = true;
      if (unsubWatchlist) { unsubWatchlist(); unsubWatchlist = null; }
      if (timer !== null) { clearTimeout(timer); timer = null; }
      if (transitionEpochRef.current !== myEpoch) return; // superseded by a newer edge meanwhile
      const current = wsRef.current ?? wsNow;
      // Only main gets the demo starter layout. Named workspaces are explicit
      // user-created canvases and must remain empty until the user chooses a
      // preset, template, or panel.
      const base = current.panels.length === 0 && workspaceName === "main"
        ? { ...current, ...PRESETS.find((p) => p.id === "trading")!.build() }
        : current;
      if (current.panels.length === 0 && workspaceName !== "main") {
        onTransitionApplied?.();
        return;
      }
      const isSymbolBearing = (id: string) => PANELS[id]?.symbolBearing ?? false;
      applyWorkspace(planDemoEntry(base, universe, isSymbolBearing));
      // Appended separately (not folded into planDemoEntry) so dockview
      // computes grid placement for it — see addPanel's own pendingRef
      // comment for why this composes correctly with the applyWorkspace call
      // just above in the same tick.
      if (!(wsRef.current?.panels ?? []).some((p) => p.panelId === "watchlist")) addPanel("watchlist");
      onTransitionApplied?.();
    };

    const initialSymbols = stores.watchlist.getSnapshot().symbols;
    if (initialSymbols.length > 0) {
      finishEntry(initialSymbols);
    } else {
      unsubWatchlist = stores.watchlist.subscribe(() => {
        const snap = stores.watchlist.getSnapshot();
        if (snap.symbols.length > 0) finishEntry(snap.symbols);
      });
      timer = setTimeout(() => finishEntry(stores.watchlist.getSnapshot().symbols), 5000);
    }

    // If mode flips again (or this AppShell unmounts) while the barrier above
    // is still waiting, drop the subscription/timer immediately instead of
    // letting them dangle up to 5s — `settled` also blocks finishEntry from
    // running twice if this races the barrier's own natural resolution.
    return () => {
      settled = true;
      if (unsubWatchlist) unsubWatchlist();
      if (timer !== null) clearTimeout(timer);
    };
  }, [sessionMode.mode, wsLoaded]);

  const scannerSnapshot = useSyncExternalStore(
    (cb) => stores.scanner.subscribe(cb),
    () => stores.scanner.getSnapshot(),
    () => stores.scanner.getSnapshot(),
  );
  const scannerView = useMemo(() => stores.scanner.currentView(), [scannerSnapshot, stores.scanner]);
  const scannerPlan = useMemo<ScannerSyncPlan>(() => {
    const current = ws;
    const sync = syncConfig;
    const sourceId = sync?.sourceWorkspaceId ?? MONITORING_WORKSPACE_ID;
    const sourceWorkspaceDoc = sourceId === MONITORING_WORKSPACE_ID ? current : sourceWorkspace;
    const source = sourceWorkspaceDoc?.panels.find((panel) =>
      panel.id === sync?.sourcePanelId && panel.panelId === "scanner");
    const panelsById = new Map(current?.panels.map((panel) => [panel.id, panel]) ?? []);
    const layoutOrder = orderedPanelIds(current?.layout);
    const panelOrder = [...layoutOrder, ...(current?.panels.map((panel) => panel.id) ?? [])];
    const seenPanels = new Set<string>();
    const slots = workspaceName === MONITORING_WORKSPACE_ID ? panelOrder.flatMap((id) => {
      if (seenPanels.has(id)) return [];
      seenPanels.add(id);
      const panel = panelsById.get(id);
      return panel?.panelId === "chart" && panel.group === null
        ? [{ id: panel.id, symbol: typeof panel.settings.symbol === "string" ? panel.settings.symbol : undefined }]
        : [];
    }) : [];
    const rankedSymbols = source
      ? rankScannerRows(scannerView.rows, readScannerSort(source.settings)).map((row) => row.symbol)
      : [];
    return planScannerSync({
      slots,
      rankedSymbols,
      enabled: workspaceName === MONITORING_WORKSPACE_ID && sync?.enabled === true,
      sourceAvailable: source !== undefined,
    });
  }, [scannerView, sourceWorkspace, syncConfig, workspaceName, ws]);
  const applyScannerPlan = useCallback((plan: ScannerSyncPlan) => {
    if (workspaceName !== MONITORING_WORKSPACE_ID || plan.patches.length === 0) return;
    const current = wsRef.current;
    if (!current?.scannerSync?.enabled || !current.scannerSync.sourcePanelId) return;
    const sourceId = current.scannerSync.sourceWorkspaceId ?? MONITORING_WORKSPACE_ID;
    const sourceDoc = sourceId === MONITORING_WORKSPACE_ID ? current : sourceWorkspaceRef.current;
    if (!sourceDoc?.panels.some((panel) => panel.id === current.scannerSync?.sourcePanelId && panel.panelId === "scanner")) return;
    const patches = new Map(plan.patches.map((patch) => [patch.slotId, patch.symbol]));
    let changed = false;
    const panels = current.panels.map((panel) => {
      const symbol = patches.get(panel.id);
      if (symbol === undefined || panel.panelId !== "chart" || panel.group !== null || panel.settings.symbol === symbol) return panel;
      changed = true;
      panelSymbols.set(panel.id, symbol);
      return { ...panel, settings: { ...panel.settings, symbol } };
    });
    if (!changed) return;
    const next = { ...current, panels };
    wsRef.current = next;
    setWs(next);
    workspaceStore.save(next);
  }, [panelSymbols, sourceWorkspace, workspaceName, workspaceStore]);
  const scannerPlanTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pendingScannerPlanRef = useRef<ScannerSyncPlan | null>(null);
  const lastScannerPlanAtRef = useRef(0);
  useEffect(() => {
    if (workspaceName !== MONITORING_WORKSPACE_ID || !ws) return;
    pendingScannerPlanRef.current = scannerPlan;
    if (scannerPlanTimerRef.current !== null) return;
    const wait = Math.max(0, 1000 - (Date.now() - lastScannerPlanAtRef.current));
    scannerPlanTimerRef.current = setTimeout(() => {
      scannerPlanTimerRef.current = null;
      const pending = pendingScannerPlanRef.current;
      pendingScannerPlanRef.current = null;
      if (!pending || pending.patches.length === 0) return;
      lastScannerPlanAtRef.current = Date.now();
      applyScannerPlan(pending);
    }, wait);
  }, [applyScannerPlan, scannerPlan, workspaceName, ws]);
  useEffect(() => () => {
    if (scannerPlanTimerRef.current !== null) clearTimeout(scannerPlanTimerRef.current);
    scannerPlanTimerRef.current = null;
    pendingScannerPlanRef.current = null;
  }, [workspaceName]);
  useEffect(() => {
    const next = new Map<string, ScannerSyncPanelState>();
    if (ws) {
      const sync = syncConfig;
      const sourceId = sync?.sourceWorkspaceId ?? MONITORING_WORKSPACE_ID;
      for (const panel of ws.panels) {
        if (panel.panelId !== "scanner") continue;
        const selected = sourceId === workspaceName
          && sync?.sourcePanelId === panel.id;
        next.set(panel.id, {
          selected,
          enabled: selected && sync?.enabled === true,
          status: workspaceName === MONITORING_WORKSPACE_ID && sync?.enabled === true
            ? scannerPlan.status
            : { kind: selected && sync?.enabled === true ? "following" : "disabled", availableCount: 0, targetCount: 0 },
          statusVisible: workspaceName === MONITORING_WORKSPACE_ID && sync?.enabled === true,
          onSelect: () => selectScannerSource(panel.id),
          onToggle: toggleScannerSync,
        });
      }
    }
    scannerSyncRuntime.replace(next);
  }, [scannerPlan, scannerSyncRuntime, selectScannerSource, syncConfig, toggleScannerSync, workspaceName, ws]);

  if (!ws) return <div style={{ padding: 12 }}>loading workspace…</div>;

  const activatePanelTarget = (panelId: string) => {
    const input = hotkeyTargetInputForPanel(panelId);
    if (input) hotkeyCoordinator.activate(input);
  };

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
    refreshOwnedTarget();
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
    refreshOwnedTarget();
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
    const settings: Record<string, unknown> = panelId === "chart" ? { timeframe: "1m" } : {};
    const config: PanelConfig = { id, panelId, group: null, settings };
    const current = wsRef.current ?? ws;
    const next = { ...current, panels: [...current.panels, config] };
    wsRef.current = next;
    setWs(next);
    workspaceStore.save(next);
    if (apiRef.current) {
      pendingRef.current.push((api) => {
        if (!api.getPanel(id)) api.addPanel({ id, component: id, title: def.title, ...dockviewPanelConstraints(panelId) });
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
    hotkeyCoordinator.clearOwned(id);
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
  // focused symbol isn't hydrated yet would mount without its saved symbol.
  // Same pendingRef deferral as addPanel: if dockview is
  // already mounted, api.fromJSON needs the new panel ids present in the
  // components map first.
  const applyWorkspace = (next: Workspace, opts?: { confirm?: string }) => {
    if (!isCurrentWorkspace(next)) {
      toast.push({ level: "danger", text: "Invalid layout" });
      return;
    }
    if (opts?.confirm && !window.confirm(opts.confirm)) return;
    const currentTarget = hotkeyCoordinator.snapshot();
    if (currentTarget?.ownerWindow === windowId && !next.panels.some((p) => p.id === currentTarget.panel)) {
      hotkeyCoordinator.clearOwned(currentTarget.panel);
    }
    linkGroups.hydrate(next.groups ?? {});
    linkGroups.hydrateVenues(next.linkVenues ?? {});
    if (workspaceName === MONITORING_WORKSPACE_ID) {
      monitoringWorkspaceRef.current = next;
      observeScannerSync(next.scannerSync);
    }
    setWs(next);
    wsRef.current = next;
    workspaceStore.save(next);
    // next.panels.length === 0 is deliberately excluded: the render right
    // after this setWs swaps <DockviewReact> out for <EmptyState> (same
    // ternary addPanel's comment above references for the reverse case),
    // unmounting/disposing the live dockview instance before this queued
    // action would ever run in its post-commit effect. Queuing an api.clear()
    // against that already-disposed instance crashes inside dockview-core
    // (Tabs.delete reads DOM state the unmount already tore down) — an
    // uncaught error with no boundary above AppShell, so it takes down the
    // whole app to a blank screen instead of the EmptyState the ternary
    // already produces on its own. No action is needed here for the
    // zero-panel case; the effect that nulls apiRef.current once ws.panels is
    // empty (below) is all the cleanup dockview's own unmount requires.
    if (apiRef.current && next.panels.length > 0) {
      // `next.layout` reflects whatever `ws.layout` held in React state — and
      // onDidLayoutChange above only ever persists a fresh layout into the
      // SAVED doc (workspaceStore.save), never back into `ws`/`wsRef`. A
      // caller that deliberately carries `current.layout` through unchanged
      // (Task 13's demo entry/revert, which only rewrites panel settings/
      // groups, never the grid) can therefore hand back a stale-or-null
      // layout here even though dockview's own grid is fine. Same shape
      // check onReady uses to decide "layout present".
      const layout = applyPanelConstraintsToLayout(next.layout, next.panels, PANELS) as { grid?: unknown } | null;
      const hasLayout = !!layout && typeof layout.grid === "object" && layout.grid !== null;
      pendingRef.current.push((api) => {
        applyingWorkspaceRef.current = true;
        try {
          api.clear();
          if (hasLayout) {
            api.fromJSON(layout as Parameters<typeof api.fromJSON>[0]);
          } else {
            // No real layout to fall back to. Dockview's OWN live toJSON()
            // is NOT a safe substitute here: Task 13's revert-with-snapshot
            // can legitimately drop a panel that isn't in the snapshot (e.g.
            // the auto-added Watchlist panel), so the panel SET in `next` may
            // differ from whatever dockview is CURRENTLY showing — reusing
            // its stale layout would reference a panel id with no component
            // factory anymore and crash. Reseed a default grid straight from
            // `next.panels` instead, same as onReady's own !restored branch;
            // correct regardless of whether the panel set changed, since it
            // doesn't reference dockview's pre-clear() state at all.
            next.panels.forEach((p, i) => {
              api.addPanel({
                id: p.id, component: p.id, title: p.panelId,
                ...dockviewPanelConstraints(p.panelId),
                ...(i === 0 ? {} : { position: { direction: i % 2 ? "right" : "below" } as const }),
              });
            });
          }
        } finally {
          applyingWorkspaceRef.current = false;
        }
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
    const current = wsRef.current ?? ws;
    if (!preset) return;
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
      if (!isCurrentLayout(result.data.layout)) {
        toast.push({ level: "danger", text: "Invalid layout" });
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

  const onNewWindow = () => setNewWindowOpen(true);
  const onOpenMonitoring = () => { setAddOpen(false); openWorkspaceWindow(MONITORING_WORKSPACE_ID); };

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
        monitoring={workspaceName === MONITORING_WORKSPACE_ID}
        scannerSyncRuntime={scannerSyncRuntime} panelSymbols={panelSymbols}
        api={panelProps.api} />,
    ]),
  );

  const onReady = (event: DockviewReadyEvent) => {
    apiRef.current = event.api;
    // Restore a previously saved dockview layout if present; otherwise seed the grid
    // from the panel list (first run — the seed's `layout` is a placeholder string).
    let restored = false;
    const layout = applyPanelConstraintsToLayout(ws.layout, ws.panels, PANELS) as { grid?: unknown } | null;
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
          ...dockviewPanelConstraints(p.panelId),
          ...(i === 0 ? {} : { position: { direction: i % 2 ? "right" : "below" } as const }),
        });
      });
    }
    event.api.onDidActivePanelChange(({ panel, origin }) => {
      if (origin === "user" && panel) activatePanelTarget(panel.id);
    });
    // Programmatic layout restore is deliberately not treated as a user
    // activation. Only the OS-focused window may seed that restored panel.
    if (document.hasFocus() && event.api.activePanel) activatePanelTarget(event.api.activePanel.id);
    // Keep ws.panels in sync when the user closes a dockview tab directly
    // (previously only the layout was re-saved on removal, leaving the closed
    // panel's config as a zombie entry in the workspace doc).
    event.api.onDidRemovePanel((panel) => {
      if (applyingWorkspaceRef.current) return; // torn down by applyWorkspace's own rebuild, not a real removal
      removePanel(panel.id);
    });
    event.api.onDidLayoutChange(() => {
      // Read via wsRef, not the `ws` this closure was created with: addPanel /
      // removePanel / applyPresetToWorkspace can change ws.panels after this
      // mount-time closure was captured, and onDidLayoutChange fires on every
      // drag/resize thereafter — saving the stale `ws` here would silently
      // drop any panel added/removed since mount from the persisted doc.
      const current = wsRef.current ?? ws;
      workspaceStore.save({ ...current, layout: event.api.toJSON() });
    });
  };

  return (
    <OpenSettingsProvider value={{ openOrderSettings: () => setSettings({ open: true, section: "orders" }) }}>
      <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
        <div style={{ position: "relative" }}>
          <TopBar workspaceName={workspaceLabel} health={stores.health} armed={armed}
            onArmToggle={() => (armed ? oc.disarm() : oc.arm())}
            onAddPanel={() => setAddOpen((v) => !v)}
            onNewWindow={onNewWindow}
            onOpenSettings={() => setSettings({ open: true, section: "general" })}
            onOpenConnection={onOpenConnection}
            onOpenPractice={() => setPracticeOpen(true)}
            targetCue={targetCueFor(hotkeyTarget)}
          />
          {addOpen && (
            <div className="popover" style={{ top: 40, right: 160, width: 580, maxHeight: "70vh", overflow: "auto" }}>
              <Catalog onAddPanel={addPanel} onApplyPreset={applyPresetToWorkspace} onOpenMonitoring={onOpenMonitoring} />
            </div>
          )}
        </div>
        <BootStatusBanner boot={stores.boot} />
        <DemoBanner session={stores.session} />
        <FeedStatusBanner health={stores.health} boot={stores.boot} engineState={engineState} onOpenConnection={onOpenConnection} />
        {showAlpacaHint && <AlpacaBackfillBanner onSetup={openAlpacaSetup} onDismiss={dismissAlpacaHint} />}
        <div style={{ flex: 1, minHeight: 0 }}>
          {ws.panels.length === 0 ? (
            <EmptyState onAddPanel={addPanel} onApplyPreset={applyPresetToWorkspace} onOpenMonitoring={onOpenMonitoring} showTryDemo={showTryDemo} onTryDemo={onTryDemo} onImportLayoutFile={importLayoutFile} />
          ) : (
            <PanelHeaderHostProvider>
              <DockviewReact components={components} onReady={onReady}
                defaultTabComponent={PanelHeaderTab} singleTabMode="fullwidth"
                disableFloatingGroups={isNativeWindow()}
                theme={mode === "light" ? themeLight : themeDark} />
            </PanelHeaderHostProvider>
          )}
        </div>
        <SettingsModal open={settings.open} section={settings.section}
          onSection={(s) => setSettings((v) => ({ ...v, section: s }))}
          onClose={() => setSettings((v) => ({ ...v, open: false }))}
          commands={commands}
          getWorkspace={() => {
            const base = wsRef.current ?? ws;
            const api = apiRef.current;
            return api && base.panels.length > 0 ? reconcileToGrid(base, api.toJSON()) : base;
          }}
          onImportWorkspace={onImportWorkspace}
          toast={toast}
          engineState={engineState}
          health={stores.health}
          exec={stores.exec}
          session={stores.session} />
        <PracticeLauncherModal open={practiceOpen} onClose={() => setPracticeOpen(false)} commands={commands} />
        <NewWindowModal open={newWindowOpen} currentId={workspaceName} commands={commands} workspaceStore={workspaceStore} onClose={() => setNewWindowOpen(false)} />
        {showVenueSetup && <VenueSetupPrompt onConfigure={configureVenueSetup} onDismiss={dismissVenueSetup} onTryDemo={onTryDemo} />}
      </div>
    </OpenSettingsProvider>
  );
}
