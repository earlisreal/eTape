import { useEffect } from "react";
import type { AckMsg } from "../../wire/contract";
import type { Stores } from "../../data/registry";
import type { LinkGroup, LinkGroups } from "../linkGroups";
import { useToasts } from "../Toast";
import { useOrderCommands } from "./useOrderCommands";
import { useOrderConfig } from "./useOrderConfig";
import { normalizeCombo, matchTemplate } from "./hotkeys";
import { resolvePlaceTemplate } from "./resolveTemplate";
import type { PlaceOrderTemplate, ManagementTemplate } from "./actionTemplate";
import { resolveVenue } from "./venueSelection";

interface Cmd { sendCommand(name: string, args: unknown): Promise<AckMsg> }

export function useHotkeys(opts: { stores: Stores; commands: Cmd; linkGroups: LinkGroups; group?: LinkGroup }): void {
  const { stores, commands, linkGroups, group = "green" } = opts;
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  const { config } = useOrderConfig(); // shared context (mounted in App via OrderConfigProvider)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = matchTemplate(config.templates, normalizeCombo(e));
      if (!t) return;
      e.preventDefault();
      const status = stores.exec.status();
      const venue = resolveVenue(group, linkGroups, config.activeVenue, status);
      const symbol = linkGroups.symbolFor(group) ?? "";

      if (t.kind === "place") {
        const armed = !!status?.masterArmed;
        if (!document.hasFocus()) return;
        if (!armed) { toast.push({ level: "warn", text: "disarmed — hotkey blocked" }); return; }
        const quote = stores.quote.get(symbol);
        if (!quote || venue === "") { toast.push({ level: "danger", text: "no venue/quote for hotkey" }); return; }
        const account = stores.exec.accounts().find((a) => a.venue === venue);
        const positionQty = stores.exec.positions().filter((p) => p.symbol === symbol && p.venue === venue).reduce((s, p) => s + p.qty, 0);
        const r = resolvePlaceTemplate(t as PlaceOrderTemplate, { venue, symbol, quote, buyingPower: account?.buyingPower ?? 0, positionQty, nowMs: Date.now() });
        for (const n of r.preCheck.notices) toast.push({ level: "warn", text: n });
        if (!r.preCheck.ok) { toast.push({ level: "danger", text: r.preCheck.errors.join(" ") }); return; }
        void oc.submit(r.args, r.flash);
        return;
      }

      // management — fires regardless of armed state (closing exposure is never gated)
      switch ((t as ManagementTemplate).action) {
        case "CancelLast": void oc.cancelLast(symbol || undefined); break;
        case "CancelAllFocused": void oc.cancelAll("focused", symbol || undefined); break;
        case "CancelAllEverything": void oc.cancelAll("everything"); break;
        case "KillSwitch": void oc.kill(); toast.push({ level: "warn", text: "KILL — cancel-all + disarm" }); break;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [stores, linkGroups, group, oc, toast, config]);
}
