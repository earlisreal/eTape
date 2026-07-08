import { useSyncExternalStore } from "react";
import type { VenueID, ExecStatus } from "../../wire/contract";
import type { Stores } from "../../data/registry";
import type { LinkGroup, LinkGroups } from "../linkGroups";
import { useOrderConfig } from "./useOrderConfig";

// The venue-resolution chain shared by the order ticket, the Account panel, and
// the hotkey engine: a grouped panel's focused venue wins, else the global
// active venue, else the first configured venue, else none. `||` (not `??`) so
// the empty-string activeVenue default falls through to the first venue.
export function resolveVenue(
  group: LinkGroup,
  linkGroups: LinkGroups,
  activeVenue: VenueID,
  status: ExecStatus | null,
): VenueID {
  return linkGroups.venueFor(group) || activeVenue || status?.venues[0]?.venue || "";
}

// Hook form for panels: returns the resolved venue, the full venue-id list, and
// a setter that writes group-focus for grouped panels or the global active venue
// for pinned panels (group === null). Subscribes to both the link bus (venue
// re-pick) and the exec store (venue list changes) so the panel re-renders.
export function useVenueSelection(
  group: LinkGroup,
  linkGroups: LinkGroups,
  stores: Stores,
): { venue: VenueID; venues: VenueID[]; selectVenue: (v: VenueID) => void } {
  const { config: orderCfg, setActiveVenue } = useOrderConfig();
  useSyncExternalStore((cb) => linkGroups.subscribe(cb), () => linkGroups.venueFor(group));
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const status = stores.exec.status();
  const venues = status?.venues.map((v) => v.venue) ?? [];
  const venue = resolveVenue(group, linkGroups, orderCfg.activeVenue, status);
  const selectVenue = (v: VenueID) => {
    if (group === null) setActiveVenue(v);   // pinned panels drive the global active venue
    else linkGroups.focusVenue(group, v);     // grouped panels write focusVenue only, leaving activeVenue untouched
  };
  return { venue, venues, selectVenue };
}
