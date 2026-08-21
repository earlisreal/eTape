import { useEffect } from "react";
import type { AckMsg } from "../../wire/contract";
import type { Stores } from "../../data/registry";
import type { HotkeyTarget } from "../hotkeyTarget";
import { modalTracker } from "../modalTracker";
import { useToasts } from "../Toast";
import { useOrderCommands } from "./useOrderCommands";
import { useOrderConfig } from "./useOrderConfig";
import { normalizeCombo, matchTemplate } from "./hotkeys";
import { fireTemplate } from "./fireTemplate";

interface Cmd { sendCommand(name: string, args: unknown): Promise<AckMsg> }

function isScoped(t: ReturnType<typeof matchTemplate>): boolean {
  return t?.kind === "place" || t?.action === "CancelLast" || t?.action === "CancelAllFocused";
}

function isEditableFocus(): boolean {
  const active = document.activeElement;
  if (!active) return false;
  const tag = active.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || (active as HTMLElement).isContentEditable;
}

export function useHotkeys(opts: { stores: Stores; commands: Cmd; target: HotkeyTarget | null }): void {
  const { stores, commands, target } = opts;
  const toast = useToasts();
  const oc = useOrderCommands(commands, stores.exec, toast);
  const { config } = useOrderConfig(); // shared context (mounted in App via OrderConfigProvider)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = matchTemplate(config.templates, normalizeCombo(e));
      if (!t) return;
      e.preventDefault();
      e.stopPropagation();
      if (e.repeat) return;

      if (isScoped(t)) {
        // Scoped bindings are deliberately quiet while the user is working in
        // a form or modal. Global emergency bindings continue below.
        if (modalTracker.isOpen() || isEditableFocus()) return;
        if (!target) {
          toast.push({ level: "warn", text: "hotkey blocked — no grouped panel target" });
          return;
        }
        if (!target.group) {
          toast.push({ level: "warn", text: "hotkey blocked — target panel is ungrouped; choose a link group" });
          return;
        }
        if (t.kind === "manage" && !target.symbol) {
          toast.push({ level: "warn", text: "hotkey blocked — target group has no symbol" });
          return;
        }
      }

      const status = stores.exec.status();
      const venue = target?.venue ?? "";
      const symbol = target?.symbol ?? "";

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
        {
          venue, symbol, quote, buyingPower: account?.buyingPower ?? 0, availableCash: account?.availableCash ?? 0,
          positionQty, armed: !!status?.masterArmed, nowMs: Date.now(),
          extHoursMarketBufferPct: config.extHoursMarketBufferPct ?? 1,
        },
        oc, toast, { gateArm: true },
      );
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [stores, target, oc, toast, config]);
}
