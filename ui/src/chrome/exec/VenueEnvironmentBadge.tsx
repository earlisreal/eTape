import type { VenueStatus } from "../../wire/contract";

type VenueEnvironment = "LIVE" | "PAPER" | "SIM";

export function venueEnvironment(venue: Pick<VenueStatus, "broker" | "env"> | undefined): VenueEnvironment | null {
  const env = venue?.env?.toLowerCase();
  if (env === "live") return "LIVE";
  if (env === "paper") return venue?.broker === "sim" ? "SIM" : "PAPER";
  return null;
}

const descriptions = {
  LIVE: "Live venue: real-money account",
  PAPER: "Paper venue: simulated orders",
  SIM: "Simulator: simulated fills",
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
