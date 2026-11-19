import type { VenueStatus } from "../../wire/contract";

type VenueEnvironment = "LIVE" | "PAPER" | "SIM" | "VST" | "DEMO";

export function venueEnvironment(venue: Pick<VenueStatus, "broker" | "env"> | undefined): VenueEnvironment | null {
  const env = venue?.env?.toLowerCase();
  if (env === "live") return "LIVE";
  if (env === "paper") {
    if (venue?.broker === "bingx-vst") return "VST";
    if (venue?.broker === "binance-demo") return "DEMO";
    return venue?.broker === "sim" || venue?.broker === "mexc-sim" ? "SIM" : "PAPER";
  }
  return null;
}

const descriptions = {
  LIVE: "Live venue: real-money account",
  PAPER: "Paper venue: simulated orders",
  SIM: "Simulator: simulated fills",
  VST: "BingX Virtual Simulated Trading",
  DEMO: "Binance Futures Demo: simulated funds",
};

export function VenueEnvironmentBadge({ environment }: { environment: VenueEnvironment | null }): JSX.Element | null {
  if (!environment) return null;
  return (
    <span className={`venue-environment-badge chip ${environment === "LIVE" ? "chip-live" : "chip-neutral"}`}
      data-testid="venue-environment-badge" aria-label={descriptions[environment]} title={descriptions[environment]}>
      {environment}
    </span>
  );
}
