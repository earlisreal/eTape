# eTape Trading Workspace

eTape presents live US-market data and trading controls while preserving the trader's chosen chart context.

## Chart Viewport

**Live View**:
The chart state in which incoming bars keep the newest displayed bar visible while preserving the trader's chosen zoom. Its default position has four empty bar-widths of right padding.
_Avoid_: Live edge, auto-scroll mode

**Future Buffer**:
Extra empty space deliberately created by panning toward future time. Incoming displayed bars consume this space without moving the viewport until only the standard four-bar right padding remains.
_Avoid_: Future-detached mode, blank bars

**Historical View**:
The chart state while the newest displayed bar is outside the viewport. Its position and zoom remain fixed as data changes; automatic movement resumes when the newest bar becomes visible again or Reset Chart View is invoked.
_Avoid_: Scrolled-back mode, detached mode

**Reset Chart View**:
The explicit action that restores the default time scale, re-enables price autoscaling, and returns the newest displayed bar to view.
_Avoid_: Jump to live, reset zoom

**No-Trade Bar**:
A completed 10-second interval with no received trade, displayed as a flat candle at the previous close with zero volume. A delayed real bar replaces it in place.
_Avoid_: Blank bar, empty placeholder, synthetic candle

**Data Gap**:
An interval known to lack trustworthy market data, including a confirmed feed interruption. It remains visually empty and is never represented by No-Trade Bars.
_Avoid_: Halt, no-trade interval

## Market Tape

**Aggressor Direction**:
The side inferred to have crossed the spread for a trade: buy, sell, or neutral when neither side can be assigned. It does not identify the market participant.
_Avoid_: Buyer, seller, trade side

**Significant Print**:
A trade whose size is unusually large relative to recent comparable trades for the same symbol and trading session. Its direction describes the aggressor side, not the identity or intent of a market participant.
_Avoid_: Big buyer, big seller, whale trade
