# eTape Trading Workspace

eTape presents live US-market data and trading controls while preserving the trader's chosen chart context.

## Workspace Layout

**Panel Group**:
A container for one or more eTape panels. A one-panel group presents a full-width Panel Header; a multi-panel group presents Tabs above the active panel's Panel Header.
_Avoid_: Pane, window

**Panel Header**:
The eTape control surface for a panel, carrying its identity and panel-level controls. It is a drag handle when alone; when its Panel Group contains peers, it appears beneath the group's Tabs.
_Avoid_: In-body header, title bar

**Top Bar**:
The global eTape control surface above the workspace, carrying application-wide time, Link Group focus, and workspace commands.
_Avoid_: Top header, app header, panel header

**Session Transition Countdown**:
A Top Bar time display showing the remaining time until the next scheduled market-session phase.
_Avoid_: Next session countdown, market open timer

**Tab**:
A selectable, draggable panel selector within a multi-panel Panel Group.
_Avoid_: Panel header

**Link Group**:
A colour-named shared focus channel through which panels follow the same symbol and venue across windows. It is independent of a Panel Group; a panel with no Link Group is pinned.
_Avoid_: Panel group, tab group

**Monitoring Workspace**:
eTape's reserved workspace for following Scanner rankings with Chart Panels. It cannot be renamed or deleted, while its Panel Groups remain user-editable.
_Avoid_: Monitoring window, monitoring layout

**Scanner Sync**:
A persistent, toggleable Monitoring Workspace mode driven by one Scanner Source that maintains pinned Chart Panel symbols from ranked Scanner results. Its top set contains as many symbols as the Monitoring Workspace has pinned Chart Panels; a Chart Panel keeps its current symbol until it leaves that set. It remains enabled but pauses when no pinned Chart Panel exists; unmatched Chart Panels retain their current symbols when the Scanner Source returns too few rows.
The Scanner Source's selected sort order, including Volume Ratio and Reported Short Interest, determines that top set.
_Avoid_: Auto load, scanner auto-refresh

**Unassigned Chart Panel**:
A pinned Chart Panel with no current symbol. In a Monitoring Workspace it displays “Waiting for Scanner Sync” until Scanner Sync assigns it a ranked symbol.
_Avoid_: Blank chart, default chart

**Layout-only Export**:
A portable Workspace Layout export that preserves panel arrangement, panel settings unrelated to symbol selection, Link Group membership, and Scanner Sync's enabled intent, but omits every panel symbol, every Link Group focused symbol, and the non-portable Scanner Source reference. An imported enabled Scanner Sync is paused until its user selects a Scanner Source. It never alters an already saved Workspace.
_Avoid_: Workspace backup, symbol-free preset

**Scanner Source**:
The Scanner Panel, in any Workspace, explicitly selected to drive Scanner Sync for the Monitoring Workspace. Only one Scanner Source is active at a time. Its identity survives closing its host window; deleting it pauses Scanner Sync.
_Avoid_: Active scanner, selected scanner

**Stock Info Panel**:
A symbol-bearing panel that displays fundamentals and news for the focused symbol of its selected Link Group. It is not a Scanner Sync target.
_Avoid_: News panel

**Operating Country**:
The company profile country associated with the issuer's primary operating or headquarters location; it is not the listing market or legal incorporation jurisdiction.
_Avoid_: Incorporation country, listing country

**Sector**:
A broad provider classification of a company's business; it is distinct from a narrower Industry classification and is not assumed to be GICS unless the source says so.
_Avoid_: Industry, sub-sector

**Industry**:
A provider-specific business classification for a company; it is narrower than Sector and is not interchangeable with it.
_Avoid_: Sector

**Free Float**:
Shares available for public trading rather than held by restricted or controlling owners.
_Avoid_: Free-float market cap

**Free-Float Market Cap**:
The market value calculated from the share price multiplied by Free Float shares.
_Avoid_: Free Float

**Reported Short Interest**:
The aggregate shares held short for a security at a provider's reporting settlement date. eTape preserves the provider-reported share count and its as-of date; it does not adjust it for subsequent stock splits. It is a periodically reported position, not daily short-sale volume, borrow availability, or a short-sell restriction.
_Avoid_: Short volume, shortable shares, borrow availability

**Reported Short Interest As-of Date**:
The provider's reporting settlement date associated with a Reported Short Interest value. It describes the delayed position report, not a live quote time or Scanner refresh time.
_Avoid_: Quote timestamp, live timestamp

**Volume Ratio**:
The ratio of current average per-minute trading volume since market open to the prior five trading days’ average per-minute trading volume. It is a multiplier, not a percentage or a same-time-of-day cumulative-volume comparison.
Scanner displays the provider-supplied number without a multiplier suffix during regular and extended sessions when it is available; an unavailable value is not zero.
_Avoid_: Relative Volume

**Volume Ratio Filter**:
A Scanner minimum Volume Ratio multiplier. It is off at zero; when active, a row with an unavailable Volume Ratio does not match.
_Avoid_: Percentage filter, volume filter

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

**Estimated LULD Band**:
A display-only, locally calculated approximation of a U.S. Limit Up-Limit Down price band derived from non-SIP market data. It can be unavailable or frozen; it is never an official LULD band, Limit State, Straddle State, Trading Pause, or order-entry control.
_Avoid_: Official LULD, halt signal

**Volume-Eligible Print**:
A Reported Print whose Trade-Report Condition makes its shares eligible for consolidated volume and tick-derived volume statistics, independently of its price eligibility.
_Avoid_: Price-forming print, valid volume

**Aggressor Direction**:
The side inferred to have crossed the spread for a trade: buy, sell, or neutral when neither side can be assigned. It does not identify the market participant.
_Avoid_: Buyer, seller, trade side

**Significant Print**:
A trade whose size is unusually large relative to recent comparable trades for the same symbol and trading session. Its direction describes the aggressor side, not the identity or intent of a market participant.
_Avoid_: Big buyer, big seller, whale trade
