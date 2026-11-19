import { useSyncExternalStore } from "react";
import type { ExecStatus, VenueID } from "../../wire/contract";
import type { Stores } from "../../data/registry";
import { venueMarket, venueSupportsMarket, type LinkGroup, type LinkGroups } from "../linkGroups";
import { cryptoExchangeLabel } from "../cryptoScanner";

export function useLinkedVenue(group: LinkGroup, linkGroups: LinkGroups, status: ExecStatus | null): VenueID {
  return useSyncExternalStore((cb) => linkGroups.subscribe(cb), () => linkGroups.venueFor(group, status)) ?? "";
}

export function useVenueSelection(group: LinkGroup, linkGroups: LinkGroups, stores: Stores) {
  const symbol = useSyncExternalStore((cb) => linkGroups.subscribe(cb), () => linkGroups.symbolFor(group));
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const status = stores.exec.status();
  const venue = useLinkedVenue(group, linkGroups, status);
  const market = venueMarket(symbol);
  const venues = status?.venues.filter(v => venueSupportsMarket(v, market)) ?? [];
  const placeholder = group === null ? "Choose a Link Group"
    : linkGroups.awaitingLiveConfirmation(group, status) ? "Confirm live account"
    : market && market !== "stocks" ? "Choose a " + cryptoExchangeLabel[market] + " account"
    : "Choose a stock account";
  const selectVenue = (next: VenueID) => {
    const currentStatus = stores.exec.status();
    const currentMarket = venueMarket(linkGroups.symbolFor(group));
    if (!group || currentMarket !== market ||
        !currentStatus?.venues.some(v => v.venue === next && venueSupportsMarket(v, currentMarket))) return;
    if (linkGroups.needsLiveConfirmation(group, next, currentStatus) &&
        !window.confirm("Switch this Link Group from paper to live trading?")) return;
    linkGroups.focusVenue(group, next);
  };
  return { venue, venues: venues.map(v => v.venue), selectVenue, placeholder };
}
