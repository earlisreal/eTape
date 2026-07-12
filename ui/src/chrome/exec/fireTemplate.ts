// Shared "fire this template" logic: resolve→preCheck→submit for place templates,
// dispatch for management templates. Extracted so the keyboard path
// (useHotkeys.ts) and the button deck (a later task) call the exact same code
// and cannot drift. `opts.gateArm` is the one behavioral knob: the keyboard
// path always passes `gateArm: true` (place orders require master-armed);
// the deck passes `gateArm: false` (deck buttons always submit — the
// point of a deck button is a deliberate, already-confirmed click).
// Management actions (cancel/kill) are never gated by armed, in either mode.
import type { Quote, VenueID } from "../../wire/contract";
import type { ActionTemplate } from "./actionTemplate";
import { resolvePlaceTemplate } from "./resolveTemplate";
import type { OrderCommands } from "./commands";
import type { ToastApi } from "../Toast";
import { bareSymbol } from "./orderStatus";

export interface FireContext {
  venue: VenueID; symbol: string; quote?: Quote | undefined;
  buyingPower: number; positionQty: number; armed: boolean; nowMs: number;
  extHoursMarketBufferPct: number;
}

export function fireTemplate(
  t: ActionTemplate,
  ctx: FireContext,
  oc: OrderCommands,
  toast: ToastApi,
  opts: { gateArm: boolean },
): void {
  if (t.kind === "place") {
    if (opts.gateArm && !ctx.armed) { toast.push({ level: "warn", text: "locked — hotkey blocked" }); return; }
    if (ctx.venue === "") {
      toast.push({ level: "danger", text: "no execution venue — set one up in Settings › Venues & creds" });
      return;
    }
    if (ctx.symbol === "") {
      toast.push({ level: "danger", text: "no symbol focused — type a symbol in the order ticket or a linked panel" });
      return;
    }
    if (!ctx.quote) {
      toast.push({ level: "danger", text: `no live quote for ${bareSymbol(ctx.symbol)} yet — waiting for market data` });
      return;
    }
    const r = resolvePlaceTemplate(t, {
      venue: ctx.venue, symbol: ctx.symbol, quote: ctx.quote,
      buyingPower: ctx.buyingPower, positionQty: ctx.positionQty, nowMs: ctx.nowMs,
      extHoursMarketBufferPct: ctx.extHoursMarketBufferPct,
    });
    for (const n of r.preCheck.notices) toast.push({ level: "warn", text: n });
    if (!r.preCheck.ok) { toast.push({ level: "danger", text: r.preCheck.errors.join(" ") }); return; }
    void oc.submit(r.args, r.flash);
    return;
  }

  // management — fires regardless of armed state (closing exposure is never gated)
  switch (t.action) {
    case "CancelLast": void oc.cancelLast(ctx.symbol || undefined); break;
    case "CancelAllFocused": void oc.cancelAll("focused", ctx.symbol || undefined); break;
    case "CancelAllEverything": void oc.cancelAll("everything"); break;
    case "KillSwitch": void oc.kill(); toast.push({ level: "warn", text: "KILL — cancel-all + lock" }); break;
  }
}
