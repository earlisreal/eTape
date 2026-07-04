import { useEffect, useRef } from "react";
import type { PanelProps } from "./registry";

// Proves wire → store → scheduler → canvas with zero React re-render: the canvas
// is mounted once; the Surface reads QuoteStore each dirty frame and paints.
export function SmokePainterPanel({ config, stores, scheduler, width, height }: PanelProps): JSX.Element {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const symbol = (config.settings.symbol as string) ?? "US.AAPL";

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d")!;
    const off = scheduler.register({
      id: `smoke:${config.id}`,
      isDirty: () => stores.quote.consumeDirty(),
      paint: () => {
        const q = stores.quote.get(symbol);
        ctx.fillStyle = "#0F1115";
        ctx.fillRect(0, 0, canvas.width, canvas.height);
        ctx.fillStyle = "#e2e8f0";
        ctx.font = "14px monospace";
        ctx.fillText(q ? `${symbol}  ${q.last}  (${q.bid}/${q.ask})` : `${symbol}  waiting…`, 10, 24);
      },
    });
    return off;
  }, [config.id, symbol, scheduler, stores]);

  return <canvas ref={canvasRef} width={width} height={height} style={{ display: "block" }} />;
}
