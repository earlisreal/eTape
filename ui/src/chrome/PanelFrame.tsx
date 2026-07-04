import { useEffect, useRef, useState } from "react";
import { ErrorBoundary } from "./ErrorBoundary";
import { PANELS, type PanelProps } from "./panels/registry";
import type { PanelConfig } from "./workspace";
import type { Stores } from "../data/registry";
import type { Scheduler } from "../render/Scheduler";
import type { LinkGroup, LinkGroups } from "./linkGroups";

const swatch = (g: LinkGroup) =>
  g === null ? "transparent" : { red: "#ef4444", green: "#22c55e", blue: "#3b82f6", yellow: "#eab308" }[g];

export function PanelFrame(
  { config, stores, scheduler, linkGroups, commands, onConfigChange }: {
    config: PanelConfig; stores: Stores; scheduler: Scheduler;
    linkGroups: LinkGroups; commands: PanelProps["commands"];
    onConfigChange: (settings: Record<string, unknown>) => void;
  },
): JSX.Element {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });

  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const r = entries[0].contentRect;
      setSize({ width: Math.floor(r.width), height: Math.floor(r.height) });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const def = PANELS[config.panelId];
  const Body = def?.component;
  const props: PanelProps = { config, stores, scheduler, width: size.width, height: size.height,
    linkGroups, commands, onConfigChange };

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "2px 8px",
        background: "#141821", borderBottom: "1px solid #1f2430", fontSize: 12 }}>
        <span style={{ width: 8, height: 8, borderRadius: 2, background: swatch(config.group) as string }} />
        <span>{config.panelId}</span>
      </div>
      <div ref={hostRef} style={{ flex: 1, minHeight: 0 }}>
        <ErrorBoundary label={config.panelId}>
          {Body ? <Body {...props} /> : <div style={{ padding: 12, color: "#64748b" }}>“{config.panelId}” — coming in a later plan</div>}
        </ErrorBoundary>
      </div>
    </div>
  );
}
