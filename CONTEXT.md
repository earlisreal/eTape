# eTape Trading Workspace

eTape presents live US-market data and trading controls while preserving the trader's chosen chart context.

## Workspace Layout

**Panel Group**:
A container for one or more eTape panels. A one-panel group presents a full-width Panel Header; a multi-panel group presents Tabs above the active panel's Panel Header.
_Avoid_: Pane, window

**Panel Header**:
The eTape control surface for a panel, carrying its identity and panel-level controls. It is a drag handle when alone; when its Panel Group contains peers, it appears beneath the group's Tabs.
_Avoid_: In-body header, title bar

**Tab**:
A selectable, draggable panel selector within a multi-panel Panel Group.
_Avoid_: Panel header

**Link Group**:
A colour-named shared focus channel through which panels follow the same symbol and venue across windows. It is independent of a Panel Group; a panel with no Link Group is pinned.
_Avoid_: Panel group, tab group

## Order Entry

**Action Template**:
A trader-authored saved recipe for placing or managing an order, available through a hotkey and/or a Deck Button.
_Avoid_: Macro, preset action

**Hotkey Deck**:
A configurable button surface embedded in an Order Ticket that exposes Action Templates without creating another execution mode.
_Avoid_: Toolbar, dockable panel

**Deck Button**:
A clickable reference to one Action Template in a Hotkey Deck; it is not an independently defined action.
_Avoid_: Deck action, macro button

**Deck Row**:
An ordered, user-managed horizontal collection of Deck Buttons in a Hotkey Deck.
_Avoid_: Strip, toolbar row

**Deck Placement**:
The sole position of an Action Template in a Deck Row; an Action Template may have zero or one Deck Placement.
_Avoid_: Copy, duplicate

**Deck Layout**:
The ordered, non-empty set of Deck Rows in an OrderConfig that determines the Hotkey Deck's button arrangement.
_Avoid_: Template order, workspace layout

**Hotkey Label Visibility**:
A Hotkey Deck-wide preference that determines whether bound hotkey combinations appear on every Deck Button; it defaults off.
_Avoid_: Per-button shortcut display, keycap setting

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
A completed 10-second interval with no statistically eligible price or volume activity, displayed as a flat candle at the previous close with zero volume. A delayed eligible bar replaces it in place.
_Avoid_: Blank bar, empty placeholder, synthetic candle

**Volume-Only Bar**:
A completed 10-second interval containing Volume-Eligible Prints but no Price-Forming Print, displayed as a flat candle at the previous last-eligible close while retaining its volume, delta, and tick count.
_Avoid_: No-trade bar, odd-lot candle

**Data Gap**:
An interval known to lack trustworthy market data, including a confirmed feed interruption. It remains visually empty and is never represented by No-Trade Bars.
_Avoid_: Halt, no-trade interval

## Chart Drawings

**Chart Drawing**:
A trader-authored annotation for one symbol, defined by one or more Drawing Anchors and retained across sessions.
_Avoid_: Indicator, overlay

**Drawing Anchor**:
A time-and-price point that defines a Chart Drawing. In the Future Buffer, it refers to a future chart position that becomes a real bar as data arrives.
_Avoid_: Screen point, marker

**Measure**:
A temporary comparison between two Drawing Anchors that displays price, percentage, and bar distance. It is not retained as a Chart Drawing.
_Avoid_: Measurement drawing, ruler

**Drawing Tool Style**:
A reusable visual default for one type of Chart Drawing—color, width, and line style—retained across symbols, panels, and sessions.
_Avoid_: Palette, per-symbol style

## Market Tape

**Reported Print**:
A transaction report received from the market-data feed, whether or not its trade-report condition makes it eligible to form consolidated price statistics.
_Avoid_: Order, raw trade

**Trade-Report Condition**:
The market-data classification attached to a Reported Print that determines how it contributes to price and volume statistics.
_Avoid_: Order type, trade type

**Price-Forming Print**:
A Reported Print whose Trade-Report Condition makes it eligible to update at least one consolidated price statistic. Price eligibility is independent of volume eligibility.
_Avoid_: Valid trade, normal trade

**Range-Eligible Print**:
A Price-Forming Print eligible to update consolidated high and low statistics.
_Avoid_: Wick trade, outlier trade

**Last-Eligible Print**:
A Price-Forming Print eligible to update consolidated open, close, and last-price state, including the execution mark.
_Avoid_: Latest print, mark trade

**Volume-Eligible Print**:
A Reported Print whose Trade-Report Condition makes its shares eligible for consolidated volume and tick-derived volume statistics, independently of its price eligibility.
_Avoid_: Price-forming print, valid volume

**Aggressor Direction**:
The side inferred to have crossed the spread for a trade: buy, sell, or neutral when neither side can be assigned. It does not identify the market participant.
_Avoid_: Buyer, seller, trade side

**Significant Print**:
A trade whose size is unusually large relative to recent comparable trades for the same symbol and trading session. Its direction describes the aggressor side, not the identity or intent of a market participant.
_Avoid_: Big buyer, big seller, whale trade
