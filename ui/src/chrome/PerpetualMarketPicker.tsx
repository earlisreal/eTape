import { useEffect, useRef, useState } from "react";
import type { PerpetualMarket } from "../gen/wsmsg";
import type { PanelProps } from "./panels/registry";
import { Button } from "./controls/Button";
import { Dialog } from "./controls/Dialog";

export function PerpetualMarketPicker({ commands, onPick, onClose, onOpenVenues }: {
  commands: PanelProps["commands"];
  onPick: (symbol: string) => void;
  onClose: () => void;
  onOpenVenues?: () => void;
}): JSX.Element {
  const [markets, setMarkets] = useState<PerpetualMarket[]>();
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [filter, setFilter] = useState("");
  const [limit, setLimit] = useState(60);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => { input.current?.focus(); }, []);

  useEffect(() => {
    let active = true;
    void commands.sendCommand("ListPerpetualMarkets", {}).then(result => {
      if (!active) return;
      if (result.status === "accepted" && Array.isArray(result.value)) {
        setMarkets(result.value as PerpetualMarket[]);
      } else {
        setError(result.reason || "Could not load USDT perpetuals.");
      }
    }, () => {
      if (active) setError("Could not load USDT perpetuals.");
    });
    return () => { active = false; };
  }, [commands, attempt]);

  const search = filter.trim().toUpperCase().replace(/[ :./_-]/g, "");
  const results = markets?.filter(m => `${m.exchange}${m.base}${m.settlement}`.includes(search));

  return <Dialog title="USDT perpetuals" onClose={onClose} width={420}
    footer={onOpenVenues && <Button onClick={onOpenVenues}>Configure crypto venues</Button>}>
    <form onSubmit={event => {
      event.preventDefault();
      if (results?.length === 1) onPick(results[0].symbol);
    }}>
      <input ref={input} aria-label="Search USDT perpetuals" type="search" className="field" placeholder="Search coin, pair or exchange"
        value={filter} onChange={event => { setFilter(event.target.value); setLimit(60); }} style={{ width: "100%" }} />
      {error ? <p role="status">{error} <Button type="button" onClick={() => {
        setError("");
        setAttempt(n => n + 1);
      }}>Retry</Button></p> : !results ? <p role="status">Loading markets…</p> : results.length === 0 ?
        <p role="status">No matching markets.</p> :
        <ul aria-label="Perpetual markets" style={{ listStyle: "none", margin: "8px 0 0", padding: 0 }}>
          {results.slice(0, limit).map(m => <li key={m.symbol}>
            <Button type="button" variant="quiet" aria-label={`${m.base}/${m.settlement} ${m.exchange}`} onClick={() => onPick(m.symbol)}
              style={{ display: "flex", justifyContent: "space-between", width: "100%", padding: "6px 4px" }}>
              <span className="mono">{m.base}/{m.settlement}</span>
              <span style={{ color: "var(--text-muted)" }}>{m.exchange}{m.tradingEnabled ? "" : " · Market data only"}</span>
            </Button>
          </li>)}
        </ul>}
      {results && results.length > limit &&
        <Button type="button" onClick={() => setLimit(n => n + 60)} style={{ marginTop: 8 }}>
          Show more ({results.length - limit} remaining)
        </Button>}
    </form>
  </Dialog>;
}
