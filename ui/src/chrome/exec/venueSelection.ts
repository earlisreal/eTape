import { useSyncExternalStore } from "react";
import type { ExecStatus, VenueID } from "../../wire/contract";
import type { Stores } from "../../data/registry";
import { venueSupportsMarket, type LinkGroup, type LinkGroups } from "../linkGroups";
import { cryptoBrokerExchange, cryptoExchange, cryptoExchangeLabel } from "../cryptoScanner";

export function useLinkedVenue(group: LinkGroup, linkGroups: LinkGroups, status: ExecStatus | null): VenueID {
  return useSyncExternalStore((cb) => linkGroups.subscribe(cb), () => linkGroups.venueFor(group, status)) ?? "";
}

export function useVenueSelection(group: LinkGroup, linkGroups: LinkGroups, stores: Stores, allCrypto = false) {
  const market = useSyncExternalStore((cb) => linkGroups.subscribe(cb), () => linkGroups.marketFor(group));
  useSyncExternalStore((cb) => stores.exec.subscribe(cb), () => stores.exec.getSnapshot());
  const status = stores.exec.status();
  const venue = useLinkedVenue(group, linkGroups, status);
  const cryptoMode = market !== undefined && market !== "stocks";
  const crossExchange = allCrypto && cryptoMode;
  const applicable = (v: NonNullable<typeof status>["venues"][number]) =>
    crossExchange ? cryptoBrokerExchange(v.broker) !== undefined : venueSupportsMarket(v, market);
  const venues = status?.venues.filter(applicable) ?? [];
  const exchange = cryptoExchange(market);
  const placeholder = group === null ? "Choose a Link Group"
    : linkGroups.awaitingLiveConfirmation(group, status) ? "Confirm live account"
    : crossExchange && status && venues.length === 0 ? "No crypto accounts configured"
    : crossExchange || market === "crypto" ? "Choose a crypto account"
    : exchange ? "Choose a " + cryptoExchangeLabel[exchange] + " account"
    : "Choose a stock account";
  const selectVenue = (next: VenueID) => {
    const currentStatus = stores.exec.status();
    const selected = currentStatus?.venues.find(v => v.venue === next && applicable(v));
    if (!group || linkGroups.marketFor(group) !== market || !selected) return;
    if (linkGroups.needsLiveConfirmation(group, next, currentStatus) &&
        !window.confirm("Switch this Link Group from paper to live trading?")) return;
    linkGroups.focusVenue(group, next, crossExchange ? cryptoBrokerExchange(selected.broker) : market);
  };
  return { market, venue, venues: venues.map(v => v.venue), selectVenue, placeholder };
}
