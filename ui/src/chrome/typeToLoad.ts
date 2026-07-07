// Pure state machine for "type-to-load": focus a symbol-bearing panel and
// type a ticker straight into its header, no click required. Kept free of
// DOM/React concerns so every capture rule is a plain table-driven unit test;
// PanelFrame.tsx is the only consumer and owns all DOM wiring (keydown
// listener placement, preventDefault/stopPropagation, rendering the draft).
export type TypeToLoadState = { editing: false } | { editing: true; draft: string };
export type TypeToLoadEvent =
  | { kind: "key"; key: string; ctrl: boolean; meta: boolean; alt: boolean }
  | { kind: "commit" }
  | { kind: "cancel" };

// Ticker characters only: letters, digits, and the dot used by dual-class
// tickers (BRK.B, BF.A). Exported so PanelFrame's keydown handler can decide
// whether a given native KeyboardEvent is one this machine would ever act on
// (e.g. to gate preventDefault/stopPropagation) without duplicating the regex.
export const PRINTABLE_SYMBOL_CHAR = /^[A-Za-z0-9.]$/;

export function canStartTypeToLoad(ctx: {
  active: boolean;
  symbolBearing: boolean;
  targetIsFormField: boolean;
  modalOpen: boolean;
}): boolean {
  return ctx.active && ctx.symbolBearing && !ctx.targetIsFormField && !ctx.modalOpen;
}

export function reduceTypeToLoad(state: TypeToLoadState, ev: TypeToLoadEvent): TypeToLoadState {
  if (ev.kind === "cancel") return { editing: false };
  if (ev.kind === "commit") return { editing: false };
  // ev.kind === "key"
  if (ev.ctrl || ev.meta || ev.alt) return state; // never capture with a modifier (order hotkeys unaffected)
  if (!state.editing) {
    return PRINTABLE_SYMBOL_CHAR.test(ev.key) ? { editing: true, draft: ev.key.toUpperCase() } : state;
  }
  if (ev.key === "Enter") return { editing: false };
  if (ev.key === "Escape") return { editing: false };
  if (ev.key === "Backspace") return { editing: true, draft: state.draft.slice(0, -1) };
  if (PRINTABLE_SYMBOL_CHAR.test(ev.key)) return { editing: true, draft: (state.draft + ev.key).toUpperCase() };
  return state; // ignore other non-printables while editing (Tab, arrows, Shift, …)
}
