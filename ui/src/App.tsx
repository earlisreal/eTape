import { useEffect, useMemo, useState } from "react";
import { WsClient, type ConnState, type ISocket } from "./wire/WsClient";
import { browserRaf } from "./render/surface";
import { Scheduler } from "./render/Scheduler";
import { makeStores, connectStores } from "./data/registry";
import { BroadcastChannelBus, LinkGroups } from "./chrome/linkGroups";
import { DemandRegistry } from "./wire/DemandRegistry";
import { WorkspaceStore } from "./chrome/workspace";
import { PANELS } from "./chrome/panels/registry";
import { AppShell } from "./chrome/AppShell";
import { ReconnectOverlay } from "./chrome/ReconnectOverlay";
import { ThemeProvider } from "./chrome/ThemeProvider";
import { ToastProvider } from "./chrome/Toast";
import { OrderConfigProvider } from "./chrome/exec/useOrderConfig";
import { SoundConfigProvider } from "./sound/SoundConfigProvider";
import { BroadcastChannelDrawingBus } from "./render/chart/drawings/store";
import type { DrawingStore } from "./render/chart/drawings/store";
import type { DrawingToolStyleStore } from "./render/chart/drawings/toolStyles";
import { useToasts } from "./chrome/Toast";
import type { TopicName } from "./wire/contract";

function DrawingsSyncBridge(
  { store, commands }: { store: DrawingStore; commands: { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }> } },
): null {
  const toast = useToasts();
  useEffect(() => {
    const off = store.connect({
      commands,
      bus: new BroadcastChannelDrawingBus(),
      onError: (reason) => toast.push({ level: "danger", text: `Drawings: ${reason}` }),
    });
    return off;
  }, [store, commands, toast]);
  return null;
}

function DrawingToolStylesSyncBridge(
  { store, commands }: { store: DrawingToolStyleStore; commands: { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown; reason?: string }> } },
): null {
  useEffect(() => store.connect({ commands }), [store, commands]);
  return null;
}

export function App({ workspaceName }: { workspaceName: string }): JSX.Element {
  const [state, setState] = useState<ConnState>("connecting");

  const { client, stores, scheduler, workspaceStore, linkGroups, demandRegistry } = useMemo(() => {
    const client = new WsClient({
      url: `ws://${location.host}/ws`,
      socketFactory: (url) => {
        // The real WebSocket delegates to whatever handlers WsClient assigns to
        // sock.onopen/onmessage/onclose (set just after this returns).
        const ws = new WebSocket(url);
        const sock: ISocket = { send: (d) => ws.send(d), close: () => ws.close(), onopen: null, onmessage: null, onclose: null };
        ws.onopen = () => sock.onopen?.();
        ws.onmessage = (e) => sock.onmessage?.(String(e.data));
        ws.onclose = () => sock.onclose?.();
        return sock;
      },
      now: () => performance.now(),
      setTimeout: (fn, ms) => window.setTimeout(fn, ms),
    });
    const stores = makeStores();
    const scheduler = new Scheduler(browserRaf, (id, err) => console.error("painter crashed", id, err));
    const workspaceStore = new WorkspaceStore(client);
    // Task 13: return the ack promise (rather than discarding it with `void`)
    // so a grouped type-to-load commit can await it via LinkGroups.focusChecked
    // and revert on a rejecting ack instead of moving the group blind.
    const linkGroups = new LinkGroups(new BroadcastChannelBus(), (group, symbol) =>
      client.sendCommand("FocusGroup", { group, symbol }),
    );
    const demandRegistry = new DemandRegistry(client);
    return { client, stores, scheduler, workspaceStore, linkGroups, demandRegistry };
  }, []);

  useEffect(() => {
    client.onState(setState);
    client.start();
    scheduler.start();
    // A workspace now starts blank and any catalog panel can be added to it later
    // (build-anything catalog, Task 6+), so we can't derive the topic set from the
    // workspace's current panel list at mount time. Instead subscribe the union of
    // every catalog panel's topics up front. This over-subscribes slightly (topics
    // for panels the user never adds) but is correct and simple; a follow-up could
    // narrow this to the union of currently-mounted panels' topics.
    const topics = new Set<TopicName>();
    for (const def of Object.values(PANELS)) {
      def.topics.forEach((t) => topics.add(t));
    }
    const disposeStores = connectStores(client, stores, [...topics]);
    const ping = window.setInterval(() => client.sendPing(), 2000);
    return () => { window.clearInterval(ping); disposeStores(); scheduler.stop(); client.stop(); };
  }, [client, stores, scheduler]);

  const commands = useMemo(() => ({
    sendCommand: (name: string, args: unknown) => client.sendCommand(name, args),
    sendQuery: (name: string, args: unknown) => client.sendQuery(name, args),
  }), [client]);

  return (
    <ThemeProvider commands={commands}>
      <ToastProvider>
        <DrawingsSyncBridge store={stores.drawings} commands={commands} />
        <DrawingToolStylesSyncBridge store={stores.drawingToolStyles} commands={commands} />
        <OrderConfigProvider commands={commands}>
          <SoundConfigProvider commands={commands}>
            <ReconnectOverlay state={state}>
              <AppShell workspaceName={workspaceName} stores={stores} scheduler={scheduler}
                workspaceStore={workspaceStore} linkGroups={linkGroups} demandRegistry={demandRegistry} commands={commands} />
            </ReconnectOverlay>
          </SoundConfigProvider>
        </OrderConfigProvider>
      </ToastProvider>
    </ThemeProvider>
  );
}
