export type LinkGroup = "red" | "green" | "blue" | "yellow" | null; // null = pinned
export interface LinkMsg { group: LinkGroup; symbol: string }

export interface LinkBus {
  post(msg: LinkMsg): void;
  onMessage(cb: (msg: LinkMsg) => void): () => void;
  close(): void;
}

export class BroadcastChannelBus implements LinkBus {
  private ch = new BroadcastChannel("etape.link");
  post(msg: LinkMsg): void { this.ch.postMessage(msg); }
  onMessage(cb: (msg: LinkMsg) => void): () => void {
    const handler = (e: MessageEvent) => cb(e.data as LinkMsg);
    this.ch.addEventListener("message", handler);
    return () => this.ch.removeEventListener("message", handler);
  }
  close(): void { this.ch.close(); }
}

// Per-group focused symbol. Local focus publishes cross-window + echoes to the
// engine; remote focus (from the bus) updates state but never re-publishes.
export class LinkGroups {
  private readonly focused = new Map<Exclude<LinkGroup, null>, string>();
  private readonly subs = new Set<() => void>();

  constructor(
    private readonly bus: LinkBus,
    private readonly onEcho: (group: Exclude<LinkGroup, null>, symbol: string) => void,
  ) {
    this.bus.onMessage((msg) => { if (msg.group) this.setLocal(msg.group, msg.symbol); });
  }

  focus(group: Exclude<LinkGroup, null>, symbol: string): void {
    this.setLocal(group, symbol);
    this.bus.post({ group, symbol });
    this.onEcho(group, symbol);
  }

  private setLocal(group: Exclude<LinkGroup, null>, symbol: string): void {
    this.focused.set(group, symbol);
    this.subs.forEach((cb) => cb());
  }

  symbolFor(group: LinkGroup): string | undefined {
    return group ? this.focused.get(group) : undefined;
  }

  subscribe(cb: () => void): () => void { this.subs.add(cb); return () => this.subs.delete(cb); }
}
