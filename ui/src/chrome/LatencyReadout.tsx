import { useSyncExternalStore } from "react";
import type { HealthStore } from "../data/HealthStore";
import type { HealthLink, LinkName, LinkStatus } from "../wire/contract";
import { useTheme } from "./ThemeProvider";
import type { Palette } from "../render/palette";

const LABEL: Record<LinkName, string> = { "ui-engine": "eng", "engine-moomoo": "moo", "engine-tz": "tz" };
const ORDER: LinkName[] = ["ui-engine", "engine-moomoo", "engine-tz"];
const dotColor = (s: LinkStatus, p: Palette): string => (s === "ok" ? p.ok : s === "degraded" ? p.warn : p.danger);

export function LatencyReadout({ health, onOpen }: { health: HealthStore; onOpen: () => void }): JSX.Element {
  const { palette } = useTheme();
  const state = useSyncExternalStore(health.subscribe.bind(health), health.getSnapshot.bind(health));
  const byName = new Map<LinkName, HealthLink>(state.links.map((l) => [l.link, l]));
  return (
    <button
      data-testid="latency-readout"
      className="ctl mono"
      onClick={onOpen}
      title="Connection status"
      style={{ gap: 10, cursor: "pointer" }}
    >
      {ORDER.map((name) => {
        const l = byName.get(name);
        return (
          <span
            key={name}
            data-testid={`lat-${LABEL[name]}`}
            style={{ display: "inline-flex", alignItems: "center", gap: 4 }}
          >
            <span
              style={{
                width: 7,
                height: 7,
                borderRadius: "50%",
                background: l ? dotColor(l.status, palette) : palette.border,
              }}
            />
            <span
              className="serif"
              style={{ fontSize: 9, letterSpacing: ".06em", textTransform: "uppercase", color: palette.textMuted }}
            >
              {LABEL[name]}
            </span>
            <span>{l && l.ms !== null ? l.ms : "—"}</span>
          </span>
        );
      })}
      <span style={{ color: palette.textMuted, fontSize: 9 }}>ms</span>
    </button>
  );
}
