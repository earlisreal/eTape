import type { VenueID } from "../wire/contract";
import type { LinkGroup } from "./linkGroups";

export const HOTKEY_TARGET_CHANNEL = "etape.hotkey-target";

export interface HotkeyTarget {
  ownerWindow: string;
  panel: string;
  group: LinkGroup;
  symbol?: string;
  venue?: VenueID;
  revision: number;
}

export interface HotkeyTargetInput {
  panel: string;
  group: LinkGroup;
  symbol?: string | undefined;
  venue?: VenueID | undefined;
}

export type HotkeyTargetMessage =
  | { type: "request"; requester: string }
  | { type: "target"; target: HotkeyTarget }
  | { type: "replay"; requester: string; target: HotkeyTarget }
  | { type: "clear"; ownerWindow: string; panel: string; revision: number };

export interface HotkeyTargetChannel {
  postMessage(message: HotkeyTargetMessage): void;
  addEventListener(type: "message", listener: (event: MessageEvent<HotkeyTargetMessage>) => void): void;
  removeEventListener(type: "message", listener: (event: MessageEvent<HotkeyTargetMessage>) => void): void;
  close(): void;
}

class NoopChannel implements HotkeyTargetChannel {
  postMessage(message: HotkeyTargetMessage): void { void message; }
  addEventListener(type: "message", listener: (event: MessageEvent<HotkeyTargetMessage>) => void): void { void type; void listener; }
  removeEventListener(type: "message", listener: (event: MessageEvent<HotkeyTargetMessage>) => void): void { void type; void listener; }
  close(): void {}
}

function defaultChannel(): HotkeyTargetChannel {
  return typeof BroadcastChannel === "function"
    ? new BroadcastChannel(HOTKEY_TARGET_CHANNEL) as unknown as HotkeyTargetChannel
    : new NoopChannel();
}

function versionKey(ownerWindow: string, panel: string): string {
  return `${ownerWindow}\u0000${panel}`;
}

export class HotkeyTargetCoordinator {
  private readonly subs = new Set<() => void>();
  private readonly onMessageBound: (event: MessageEvent<HotkeyTargetMessage>) => void;
  private current: HotkeyTarget | null = null;
  private latestRevision = 0;
  private latestKey = "";
  private clock = 0;
  private closed = false;

  constructor(
    private readonly ownerWindow: string,
    private readonly channel: HotkeyTargetChannel = defaultChannel(),
  ) {
    this.onMessageBound = (event) => this.onMessage(event.data);
    this.channel.addEventListener("message", this.onMessageBound);
    this.channel.postMessage({ type: "request", requester: ownerWindow });
  }

  snapshot(): HotkeyTarget | null { return this.current; }

  subscribe(cb: () => void): () => void {
    this.subs.add(cb);
    return () => this.subs.delete(cb);
  }

  activate(input: HotkeyTargetInput): HotkeyTarget {
    const target = this.makeTarget(input);
    this.acceptTarget(target);
    this.channel.postMessage({ type: "target", target });
    return target;
  }

  updateOwned(panel: string, input: Omit<HotkeyTargetInput, "panel">): boolean {
    const current = this.current;
    if (!current || current.ownerWindow !== this.ownerWindow || current.panel !== panel) return false;
    if (current.group === input.group && current.symbol === input.symbol && current.venue === input.venue) return false;
    this.activate({ panel, ...input });
    return true;
  }

  clearOwned(panel: string): boolean {
    const current = this.current;
    if (!current || current.ownerWindow !== this.ownerWindow || current.panel !== panel) return false;
    const revision = this.nextRevision();
    this.current = null;
    this.latestRevision = revision;
    this.latestKey = versionKey(this.ownerWindow, panel);
    this.notify();
    this.channel.postMessage({ type: "clear", ownerWindow: this.ownerWindow, panel, revision });
    return true;
  }

  close(): void {
    if (this.closed) return;
    this.clearOwned(this.current?.panel ?? "");
    this.closed = true;
    this.channel.removeEventListener("message", this.onMessageBound);
    this.channel.close();
  }

  private makeTarget(input: HotkeyTargetInput): HotkeyTarget {
    return {
      ownerWindow: this.ownerWindow,
      panel: input.panel,
      group: input.group,
      ...(input.symbol === undefined ? {} : { symbol: input.symbol }),
      ...(input.venue === undefined ? {} : { venue: input.venue }),
      revision: this.nextRevision(),
    };
  }

  private nextRevision(): number {
    this.clock = Math.max(this.clock, this.latestRevision) + 1;
    return this.clock;
  }

  private onMessage(message: HotkeyTargetMessage): void {
    if (this.closed) return;
    if (message.type === "request") {
      if (this.current) this.channel.postMessage({ type: "replay", requester: message.requester, target: this.current });
      return;
    }
    if (message.type === "replay") {
      if (message.requester === this.ownerWindow) this.acceptTarget(message.target);
      return;
    }
    if (message.type === "target") {
      this.acceptTarget(message.target);
      return;
    }
    this.acceptClear(message.ownerWindow, message.panel, message.revision);
  }

  private acceptTarget(target: HotkeyTarget): void {
    this.clock = Math.max(this.clock, target.revision);
    const key = versionKey(target.ownerWindow, target.panel);
    if (!this.isNewer(target.revision, key)) return;
    this.current = Object.freeze({ ...target });
    this.latestRevision = target.revision;
    this.latestKey = key;
    this.notify();
  }

  private acceptClear(ownerWindow: string, panel: string, revision: number): void {
    this.clock = Math.max(this.clock, revision);
    const current = this.current;
    const key = versionKey(ownerWindow, panel);
    if (!this.isNewer(revision, key)) return;
    this.latestRevision = revision;
    this.latestKey = key;
    // Keep the tombstone even when another window has already won the global
    // target. Otherwise a delayed replay of this cleared owner could pass the
    // revision check and resurrect its stale panel.
    if (current?.ownerWindow === ownerWindow && current.panel === panel) {
      this.current = null;
      this.notify();
    }
  }

  private isNewer(revision: number, key: string): boolean {
    return revision > this.latestRevision || (revision === this.latestRevision && key > this.latestKey);
  }

  private notify(): void { this.subs.forEach((cb) => cb()); }
}
