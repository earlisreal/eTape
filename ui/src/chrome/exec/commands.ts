// Typed order-command client. Every method wraps the correlated command adapter;
// submit registers the optimistic PendingNew row (keyed by the ack's orderId) and
// raises the flash/block toast. Cancel-all/last are composed from CancelOrder over
// the working set — the engine's token buckets pace the burst.
import type { AckMsg, SubmitOrderArgs, ReplaceOrderArgs, VenueID } from "../../wire/contract";
import type { ExecStore } from "../../data/ExecStore";
import type { ToastApi } from "../Toast";

export interface CommandAdapter { sendCommand(name: string, args: unknown): Promise<AckMsg> }
export interface OrderCommandsDeps { cmd: CommandAdapter; exec: ExecStore; toast: ToastApi; now: () => number }

export class OrderCommands {
  constructor(private readonly d: OrderCommandsDeps) {}

  async submit(args: SubmitOrderArgs, flash: string): Promise<void> {
    const ack = await this.d.cmd.sendCommand("SubmitOrder", args);
    if (ack.status === "blocked") { this.d.toast.push({ level: "danger", text: `Blocked: ${ack.reason ?? "unknown"}` }); return; }
    if (ack.orderId) this.d.exec.addOptimistic({ args, id: ack.orderId, createdMs: this.d.now() });
    this.d.toast.push({ level: "info", text: flash });
  }

  async cancel(venue: VenueID, orderId: string): Promise<void> { await this.d.cmd.sendCommand("CancelOrder", { venue, orderId }); }
  async replace(args: ReplaceOrderArgs): Promise<void> { await this.d.cmd.sendCommand("ReplaceOrder", args); }
  async flatten(venue: VenueID): Promise<void> { await this.d.cmd.sendCommand("Flatten", { venue }); }

  async arm(venue?: VenueID): Promise<void> { await this.d.cmd.sendCommand("Arm", venue ? { venue } : {}); }
  async disarm(venue?: VenueID): Promise<void> { await this.d.cmd.sendCommand("Disarm", venue ? { venue } : {}); }
  async kill(venue?: VenueID): Promise<void> { await this.d.cmd.sendCommand("KillSwitch", venue ? { venue } : {}); }

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
