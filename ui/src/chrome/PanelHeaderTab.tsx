import { useContext, useLayoutEffect, useRef, useSyncExternalStore } from "react";
import type { IDockviewPanelHeaderProps } from "dockview";
import { DockviewDefaultTab } from "dockview-react";
import { PanelHeaderHostContext } from "./panels/headerSlot";

export function PanelHeaderTab(props: IDockviewPanelHeaderProps): JSX.Element {
  const { api, containerApi, tabLocation } = props;
  const registry = useContext(PanelHeaderHostContext);
  const hostRef = useRef<HTMLDivElement | null>(null);
  const grouped = useSyncExternalStore(
    (listener) => {
      const disposable = containerApi.onDidLayoutChange(listener);
      return () => disposable.dispose();
    },
    () => api.group.panels.length > 1,
    () => api.group.panels.length > 1,
  );

  useLayoutEffect(() => {
    const host = hostRef.current;
    if (grouped || tabLocation !== "header" || !registry || !host) return;
    return registry.register(api.id, host);
  }, [api.id, grouped, registry, tabLocation]);

  if (grouped) {
    return <DockviewDefaultTab {...props} data-testid={`panel-tab-${api.id}`} />;
  }

  if (tabLocation === "headerOverflow") {
    return <span className="etape-panel-tab-overflow" data-testid="panel-tab-overflow">{api.title}</span>;
  }
  return <div ref={hostRef} className="etape-panel-tab-host" data-testid={`panel-tab-${api.id}`} />;
}
