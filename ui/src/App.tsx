import { useEffect, useMemo, useState } from "react";
import { WsClient, type ConnState, type ISocket } from "./wire/WsClient";
import { browserRaf } from "./render/surface";
import { Scheduler } from "./render/Scheduler";
import { makeStores, connectStores } from "./data/registry";
import { BroadcastChannelBus, LinkGroups } from "./chrome/linkGroups";
import { WorkspaceStore } from "./chrome/workspace";
import { PANELS } from "./chrome/panels/registry";
import { AppShell } from "./chrome/AppShell";
import { ReconnectOverlay } from "./chrome/ReconnectOverlay";
import { ThemeProvider } from "./chrome/ThemeProvider";
import { ToastProvider } from "./chrome/Toast";
import { OrderConfigProvider } from "./chrome/exec/useOrderConfig";
import { SoundConfigProvider } from "./sound/SoundConfigProvider";
import type { TopicName } from "./wire/contract";

export function App({ workspaceName }: { workspaceName: string }): JSX.Element {
  const [state, setState] = useState<ConnState>("connecting");

  const { client, stores, scheduler, workspaceStore, linkGroups } = useMemo(() => {
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
    const linkGroups = new LinkGroups(new BroadcastChannelBus(), (group, symbol) => {
      void client.sendCommand("FocusGroup", { group, symbol });
    });
    return { client, stores, scheduler, workspaceStore, linkGroups };
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

  const commands = {
    sendCommand: (name: string, args: unknown) => client.sendCommand(name, args),
    sendQuery: (name: string, args: unknown) => client.sendQuery(name, args),
  };

  return (
    <ThemeProvider commands={commands}>
      <ToastProvider>
        <OrderConfigProvider commands={commands}>
          <SoundConfigProvider commands={commands}>
            <ReconnectOverlay state={state}>
              <AppShell workspaceName={workspaceName} stores={stores} scheduler={scheduler}
                workspaceStore={workspaceStore} linkGroups={linkGroups} commands={commands} />
            </ReconnectOverlay>
          </SoundConfigProvider>
        </OrderConfigProvider>
      </ToastProvider>
    </ThemeProvider>
  );
}
