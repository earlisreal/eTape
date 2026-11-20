import { useCallback, useEffect, useMemo, useState, type RefObject } from "react";
import type { CommandClient } from "./catalogs";
import { binanceDemoExchange, cryptoExchange, cryptoExchangeKey, defaultCryptoExchange, rankCryptoScanner, readCryptoScanner, resolveScannerExchange, type CryptoExchange, useCryptoPreference, useCryptoScannerData } from "./cryptoScanner";
import { savedLinkMarket } from "./linkGroups";
import { buildCryptoMonitoringWorkspace } from "./presets";
import { planScannerSync, ScannerSyncRuntime, type PanelSymbolRuntime, type ScannerSyncPanelState } from "./scannerSync";
import { CRYPTO_MONITORING_WORKSPACE_ID as targetId, type PanelConfig, type Workspace, type WorkspaceStore } from "./workspace";

function exchangeForPanel(panel: PanelConfig, workspace: Workspace | null | undefined, appDefault: CryptoExchange) {
  const group = panel.group;
  const market = group ? savedLinkMarket(workspace?.groups?.[group], workspace?.linkMarkets?.[group]?.emptyMarket) : undefined;
  return resolveScannerExchange(readCryptoScanner(panel.settings).exchange, market, appDefault);
}

function useWatchedWorkspace(store: WorkspaceStore, id: string | undefined) {
  const [loaded, setLoaded] = useState<Workspace>();
  useEffect(() => {
    if (!id) return;
    let alive = true;
    const refresh = () => { void store.load(id).then(doc => { if (alive) setLoaded(doc); }).catch(() => {}); };
    const unsubscribe = store.watch(id, refresh);
    refresh();
    return () => { alive = false; unsubscribe(); };
  }, [id, store]);
  return loaded?.name === id ? loaded : undefined;
}

export function useCryptoFollowing({ name, workspace, current, apply, store, commands, connected, symbols, orderedIds }: {
  name: string;
  workspace: Workspace | null;
  current: RefObject<Workspace | null>;
  apply: (next: Workspace) => void;
  store: WorkspaceStore;
  commands: CommandClient;
  connected: boolean;
  symbols: PanelSymbolRuntime;
  orderedIds: (layout: unknown) => string[];
}) {
  const runtime = useMemo(() => new ScannerSyncRuntime(), []);
  const localPanels = useMemo(() => workspace?.panels.filter(panel => panel.panelId === "crypto-scanner") ?? [], [workspace]);
  const isTarget = name === targetId;
  const otherTarget = useWatchedWorkspace(store, !isTarget && localPanels.length > 0 ? targetId : undefined);
  const persistedTarget = useWatchedWorkspace(store, isTarget ? targetId : undefined);
  const sync = (isTarget ? workspace : otherTarget)?.scannerSync;
  const sourceWorkspaceId = sync?.sourceWorkspaceId ?? targetId;
  const otherSource = useWatchedWorkspace(store, isTarget && sourceWorkspaceId !== name ? sourceWorkspaceId : undefined);
  const sourceWorkspace = sourceWorkspaceId === name ? workspace : otherSource;
  const source = sourceWorkspace?.panels.find(panel =>
    panel.id === sync?.sourcePanelId && panel.panelId === "crypto-scanner");
  const needed = localPanels.length > 0 || (isTarget && sync?.enabled === true && !!source);
  const preference = useCryptoPreference(commands, cryptoExchangeKey, needed && connected);
  const exchange = cryptoExchange(preference.value) ?? defaultCryptoExchange;
  const sourceExchange = source ? exchangeForPanel(source, sourceWorkspace, exchange) : undefined;
  const requested = new Set(localPanels.map(panel => exchangeForPanel(panel, workspace, exchange)));
  if (isTarget && sync?.enabled && sourceExchange) requested.add(sourceExchange);
  const mexc = useCryptoScannerData(commands, connected, requested.has("MEXC"), "MEXC");
  const binance = useCryptoScannerData(commands, connected, requested.has("BINANCE"), "BINANCE");
  const binanceDemo = useCryptoScannerData(commands, connected, requested.has(binanceDemoExchange), binanceDemoExchange);
  const bingx = useCryptoScannerData(commands, connected, requested.has("BINGX"), "BINGX");
  const data = useMemo(() => ({
    defaultExchange: exchange,
    byExchange: { MEXC: mexc, BINANCE: binance, [binanceDemoExchange]: binanceDemo, BINGX: bingx },
  }), [exchange, mexc, binance, binanceDemo, bingx]);
  const sourceSnapshot = sourceExchange ? data.byExchange[sourceExchange].snapshot : undefined;

  const save = useCallback((next: Workspace) => { apply(next); store.save(next); }, [apply, store]);
  useEffect(() => {
    const base = current.current;
    if (!isTarget || !base || !persistedTarget || JSON.stringify(base.scannerSync) === JSON.stringify(persistedTarget.scannerSync)) return;
    const next = { ...base };
    if (persistedTarget.scannerSync) next.scannerSync = persistedTarget.scannerSync;
    else delete next.scannerSync;
    apply(next);
  }, [apply, current, isTarget, persistedTarget]);

  const select = useCallback(async (panelId: string, enabled: boolean) => {
    const base = isTarget ? current.current : await store.load(targetId, buildCryptoMonitoringWorkspace());
    if (!base) return;
    const next = { ...base, scannerSync: { enabled, sourceWorkspaceId: name, sourcePanelId: panelId } };
    if (isTarget) save(next);
    else store.save(next);
  }, [current, isTarget, name, save, store]);

  const plan = useMemo(() => {
    const chartById = new Map(workspace?.panels.filter(panel => panel.panelId === "chart" && panel.group === null)
      .map(panel => [panel.id, panel]));
    const ids = [...new Set([...orderedIds(workspace?.layout), ...chartById.keys()])];
    const slots = isTarget ? ids.flatMap(id => {
      const panel = chartById.get(id);
      return panel ? [{ id, symbol: typeof panel.settings.symbol === "string" ? panel.settings.symbol : undefined }] : [];
    }) : [];
    return planScannerSync({
      slots, enabled: sync?.enabled === true, sourceAvailable: !!source && !!sourceSnapshot,
      rankedSymbols: source && sourceSnapshot ? rankCryptoScanner(sourceSnapshot.rows, readCryptoScanner(source.settings)).map(row => row.symbol) : [],
    });
  }, [sourceSnapshot, isTarget, orderedIds, source, sync?.enabled, workspace]);

  useEffect(() => {
    const base = current.current;
    if (!isTarget || !base?.scannerSync?.enabled || plan.patches.length === 0) return;
    const patches = new Map(plan.patches.map(patch => [patch.slotId, patch.symbol]));
    let changed = false;
    const panels = base.panels.map(panel => {
      const symbol = patches.get(panel.id);
      if (!symbol || panel.panelId !== "chart" || panel.group !== null || panel.settings.symbol === symbol) return panel;
      symbols.set(panel.id, symbol);
      changed = true;
      return { ...panel, settings: { ...panel.settings, symbol } };
    });
    if (changed) save({ ...base, panels });
  }, [current, isTarget, plan, save, symbols]);

  useEffect(() => {
    const states = new Map<string, ScannerSyncPanelState>();
    for (const panel of localPanels) {
      const selected = sourceWorkspaceId === name && sync?.sourcePanelId === panel.id;
      states.set(panel.id, {
        selected, enabled: selected && sync?.enabled === true, status: plan.status, statusVisible: isTarget,
        onSelect: () => { void select(panel.id, true); },
        onToggle: () => { void select(panel.id, !sync?.enabled); },
      });
    }
    runtime.replace(states);
  }, [isTarget, localPanels, name, plan.status, runtime, select, sourceWorkspaceId, sync]);

  return { runtime, data };
}
