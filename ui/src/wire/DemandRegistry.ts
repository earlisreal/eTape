import type { AckMsg } from "./contract";
import type { ConnState } from "./WsClient";

export type DemandProfile = "watch" | "focused" | "interest";

interface DemandClient {
  sendCommand(name: string, args: unknown): Promise<AckMsg>;
  onState(cb: (s: ConnState) => void): void;
}

const ACCEPTED: AckMsg = { kind: "ack", corrId: "", status: "accepted" };

// DemandRegistry drives per-panel EnsureSymbol/ReleaseSymbol commands and keeps
// the UI as the source of truth for what's on screen: engine demand state is
// in-memory, so on reconnect (WS drop or engine restart) we re-announce every
// live demand. Owned by App.tsx alongside LinkGroups; one instance per client
// connection (demands are connection-scoped engine-side, so multi-window needs
// no coordination).
export class DemandRegistry {
  private readonly live = new Map<string, { symbol: string; profile: DemandProfile }>();

  constructor(private readonly client: DemandClient) {
    this.client.onState((s) => {
      if (s === "open") this.reannounce();
    });
  }

  // ensure subscribes a panel's symbol. Returns the ack so a gated commit path
  // can revert on rejection. An unchanged symbol+profile is a no-op that
  // resolves accepted (the engine ensure is an idempotent upsert anyway).
  async ensure(panelId: string, symbol: string, profile: DemandProfile): Promise<AckMsg> {
    const cur = this.live.get(panelId);
    if (cur && cur.symbol === symbol && cur.profile === profile) return ACCEPTED;
    const ack = await this.client.sendCommand("EnsureSymbol", { demandId: panelId, symbol, profile });
    if (ack.status === "accepted") this.live.set(panelId, { symbol, profile });
    return ack;
  }

  release(panelId: string): void {
    if (!this.live.has(panelId)) return;
    this.live.delete(panelId);
    void this.client.sendCommand("ReleaseSymbol", { demandId: panelId });
  }

  private reannounce(): void {
    for (const [panelId, { symbol, profile }] of this.live) {
      void this.client.sendCommand("EnsureSymbol", { demandId: panelId, symbol, profile });
    }
  }
}
