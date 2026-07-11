// The hotkey deck: a row of clickable preset buttons under the order ticket's
// manual BUY/SELL/SHORT/COVER row (Strip 4). Reads only `deck: true` templates
// from the shared order config (useOrderConfig) so it live-updates the moment
// Settings saves a new deck layout — venue/symbol/quote/etc. arrive as props
// from OrderTicketPanel rather than being re-derived here, keeping this
// component a dumb rendering + click-dispatch layer.
//
// Every click fires through the same fireTemplate() the keyboard path
// (useHotkeys.ts) uses, but always with `gateArm: false` — deck buttons are a
// deliberate, already-confirmed click and are never client-side gated on
// armed state (matching the ticket's own Buy/Sell/Short/Cover buttons; the
// engine's arm gate still rejects + toasts server-side if disarmed).
import type { Quote, VenueID } from "../../wire/contract";
import type { ActionTemplate } from "../exec/actionTemplate";
import type { OrderCommands } from "../exec/commands";
import type { ToastApi } from "../Toast";
import { useTheme } from "../ThemeProvider";
import { useOrderConfig } from "../exec/useOrderConfig";
import { fireTemplate } from "../exec/fireTemplate";
import { Keycap } from "../exec/Keycap";

export interface HotkeyDeckProps {
  venue: VenueID; symbol: string; quote?: Quote | undefined;
  buyingPower: number; positionQty: number;
  oc: OrderCommands; toast: ToastApi;
  onOpenSettings: () => void;
}

// deckColor is an explicit override; "auto" (or absent) falls back to the
// side for place templates, and to danger-for-KillSwitch/neutral-otherwise
// for management templates — same family as the ticket's own side-buy/
// side-sell tones, extended with the two new deck-only tones.
export function deckToneClass(t: ActionTemplate): string {
  const c = t.deckColor ?? "auto";
  if (c === "green") return "side side-buy";
  if (c === "red") return "side side-sell";
  if (c === "bronze") return "side side-bronze";
  if (c === "neutral") return "side side-neutral";
  if (c === "danger") return "side side-danger";
  if (t.kind === "manage") return t.action === "KillSwitch" ? "side side-danger" : "side side-neutral";
  return t.side === "BUY" || t.side === "COVER" ? "side side-buy" : "side side-sell";
}

export function HotkeyDeck(
  { venue, symbol, quote, buyingPower, positionQty, oc, toast, onOpenSettings }: HotkeyDeckProps,
): JSX.Element {
  const { palette } = useTheme();
  const { config } = useOrderConfig();
  const deckTemplates = config.templates.filter((t) => t.deck);

  if (deckTemplates.length === 0) {
    return (
      <button type="button" data-testid="deck-empty" onClick={onOpenSettings}
        style={{
          width: "100%", padding: "6px", borderRadius: 4, cursor: "pointer", fontSize: 11,
          border: `1px dashed ${palette.borderStrong}`, background: "transparent", color: palette.textMuted,
        }}>
        + Add preset buttons
      </button>
    );
  }

  return (
    <div style={{ display: "flex", flexWrap: "wrap", gap: 3 }}>
      {deckTemplates.map((t) => (
        <button key={t.id} type="button" data-testid={`deck-${t.id}`} className={deckToneClass(t)}
          onClick={() => fireTemplate(
            t, { venue, symbol, quote, buyingPower, positionQty, armed: false, nowMs: Date.now() },
            oc, toast, { gateArm: false },
          )}
          style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
          <span>{t.label}</span>
          {t.hotkey ? <Keycap combo={t.hotkey} /> : null}
        </button>
      ))}
    </div>
  );
}
