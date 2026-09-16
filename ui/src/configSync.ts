// The engine config store is shared, but each browser window has its own UI
// provider/store. Broadcast only the changed key; receivers re-read the
// persisted value so the engine remains the last-write-wins authority.
const CONFIG_CHANNEL = "etape.config";

export interface ConfigSync {
  notify(): void;
  close(): void;
}

export function openConfigSync(key: string, onChange: () => void): ConfigSync {
  if (typeof BroadcastChannel !== "function") return { notify: () => {}, close: () => {} };

  const channel = new BroadcastChannel(CONFIG_CHANNEL);
  const onMessage = (event: MessageEvent<unknown>) => {
    const message = event.data;
    if (message && typeof message === "object" && (message as { key?: unknown }).key === key) onChange();
  };
  channel.addEventListener("message", onMessage);
  return {
    notify: () => channel.postMessage({ key }),
    close: () => {
      channel.removeEventListener("message", onMessage);
      channel.close();
    },
  };
}
