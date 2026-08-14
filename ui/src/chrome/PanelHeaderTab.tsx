import { useContext, useLayoutEffect, useRef } from "react";
import type { IDockviewPanelHeaderProps } from "dockview";
import { PanelHeaderHostContext } from "./panels/headerSlot";

export function PanelHeaderTab({ api, tabLocation }: IDockviewPanelHeaderProps): JSX.Element {
  const registry = useContext(PanelHeaderHostContext);
  const hostRef = useRef<HTMLDivElement | null>(null);

  useLayoutEffect(() => {
    const host = hostRef.current;
    if (tabLocation !== "header" || !registry || !host) return;
    return registry.register(api.id, host);
  }, [api.id, registry, tabLocation]);

  if (tabLocation === "headerOverflow") {
    return <span className="etape-panel-tab-overflow" data-testid="panel-tab-overflow">{api.title}</span>;
  }
  return <div ref={hostRef} className="etape-panel-tab-host" data-testid={`panel-tab-${api.id}`} />;
}
