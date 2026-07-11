import { useEffect } from "react";
import type { AckMsg } from "../../wire/contract";
import type { Stores } from "../../data/registry";
import type { LinkGroup, LinkGroups } from "../linkGroups";
import { useToasts } from "../Toast";
import { useOrderCommands } from "./useOrderCommands";
import { useOrderConfig } from "./useOrderConfig";
import { normalizeCombo, matchTemplate } from "./hotkeys";
import { fireTemplate } from "./fireTemplate";
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

      // Keyboard-specific: place orders require OS window focus (never gated
      // for management templates — closing exposure is never gated on focus
      // either). This guard is intentionally NOT part of fireTemplate, since
      // the deck (a later task) fires from an already-focused click.
      if (t.kind === "place" && !document.hasFocus()) return;

      const quote = stores.quote.get(symbol);
      const account = stores.exec.accounts().find((a) => a.venue === venue);
      const positionQty = stores.exec.positions().filter((p) => p.symbol === symbol && p.venue === venue).reduce((s, p) => s + p.qty, 0);
      fireTemplate(
        t,
        { venue, symbol, quote, buyingPower: account?.buyingPower ?? 0, positionQty, armed: !!status?.masterArmed, nowMs: Date.now() },
        oc, toast, { gateArm: true },
      );
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [stores, linkGroups, group, oc, toast, config]);
}
