// Typed order-command client. Every method wraps the correlated command adapter;
// submit registers the optimistic PendingNew row (keyed by the ack's orderId) and
// raises the flash/block/unknown-outcome toast. Cancel-all/last are composed from CancelOrder over
// the working set — the engine's token buckets pace the burst.
import type { AckMsg, SubmitOrderArgs, ReplaceOrderArgs, VenueID } from "../../wire/contract";
import type { ExecStore } from "../../data/ExecStore";
import type { ToastApi } from "../Toast";
import type { SoundApi } from "../../sound/SoundEngine";

export interface CommandAdapter { sendCommand(name: string, args: unknown): Promise<AckMsg> }
export interface OrderCommandsDeps { cmd: CommandAdapter; exec: ExecStore; toast: ToastApi; now: () => number; sound?: SoundApi }

// Humanizes the engine's raw gate.Evaluate/exec.Core block reasons for the
// blocked-order toast. Reasons not listed here (most of them — the gate has
// many caps) fall back to the verbatim ack.reason.
const REASON_TEXT: Record<string, string> = {
  "no gate config for venue": "no risk limits configured — set them in Settings › Venues",
  "master disarmed": "trading is locked — unlock it in the top bar",
};

function ambiguousText(venue: VenueID): string {
  return `Outcome unknown (${venue}) — connection was lost after the request was sent. Verify Open Orders / position before submitting again.`;
}

const KILL_SUCCESS_TEXT = "KILL — cancel-all + lock";
const KILL_UNKNOWN_TEXT = "KILL outcome unknown — connection was lost. Verify open orders and positions immediately.";

export class OrderCommands {
  constructor(private readonly d: OrderCommandsDeps) {}

  async submit(args: SubmitOrderArgs, flash: string): Promise<void> {
    const ack = await this.d.cmd.sendCommand("SubmitOrder", args);
    if (ack.ambiguous) {
      this.d.toast.push({ level: "warn", text: ambiguousText(args.venue) });
      return;
    }
    if (ack.status === "blocked") {
      const reason = REASON_TEXT[ack.reason ?? ""] ?? ack.reason ?? "unknown";
      this.d.toast.push({ level: "danger", text: `Blocked (${args.venue}): ${reason}` });
      this.d.sound?.orderRejected();
      return;
    }
    if (ack.orderId) this.d.exec.addOptimistic({ args, id: ack.orderId, createdMs: this.d.now() });
    this.d.sound?.orderPlaced(args.side);
    this.d.toast.push({ level: "info", text: flash });
  }

  async cancel(venue: VenueID, orderId: string): Promise<void> {
    const ack = await this.d.cmd.sendCommand("CancelOrder", { venue, orderId });
    if (ack.ambiguous) {
      this.d.toast.push({ level: "warn", text: ambiguousText(venue) });
      return;
    }
    if (ack.status === "blocked") this.d.sound?.orderRejected();
  }
  async replace(args: ReplaceOrderArgs): Promise<void> {
    const ack = await this.d.cmd.sendCommand("ReplaceOrder", args);
    if (ack.ambiguous) {
      this.d.toast.push({ level: "warn", text: ambiguousText(args.venue) });
      return;
    }
    if (ack.status === "blocked") this.d.sound?.orderRejected();
  }
  async flatten(venue: VenueID): Promise<void> {
    const ack = await this.d.cmd.sendCommand("Flatten", { venue });
    if (ack.ambiguous) {
      this.d.toast.push({ level: "warn", text: ambiguousText(venue) });
      return;
    }
    if (ack.status === "blocked") this.d.sound?.orderRejected();
    else this.d.sound?.orderPlaced("SELL"); // risk-off: falling pitch
  }

  async arm(): Promise<void> { await this.d.cmd.sendCommand("Arm", {}); }
  async disarm(): Promise<void> { await this.d.cmd.sendCommand("Disarm", {}); }
  async kill(venue?: VenueID): Promise<void> {
    try {
      const ack = await this.d.cmd.sendCommand("KillSwitch", venue ? { venue } : {});
      if (ack.ambiguous) {
        this.d.toast.push({ level: "warn", text: KILL_UNKNOWN_TEXT });
        return;
      }
      if (ack.status === "accepted") {
        this.d.toast.push({ level: "warn", text: KILL_SUCCESS_TEXT });
        return;
      }
      this.d.toast.push({ level: "danger", text: `Kill Switch failed: ${ack.reason ?? "unknown"}` });
    } catch (err: unknown) {
      const reason = err instanceof Error ? err.message : "connection was lost";
      this.d.toast.push({ level: "warn", text: `KILL outcome unknown — ${reason}. Verify open orders and positions immediately.` });
    }
  }

  async cancelLast(symbol?: string): Promise<void> {
    const working = this.d.exec.workingOrdersFor(symbol);
    if (working.length === 0) return;
    const last = working.reduce((a, b) => (b.createdMs > a.createdMs ? b : a));
    await this.cancel(last.venue, last.id);
  }

  async cancelAll(scope: "focused" | "everything", symbol?: string): Promise<void> {
    const working = this.d.exec.workingOrdersFor(scope === "focused" ? symbol : undefined);
    await Promise.all(working.map((o) => this.cancel(o.venue, o.id)));
  }
}
