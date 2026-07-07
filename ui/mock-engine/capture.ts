// Captures live engine frames into the mock-engine Fixture format. Point it at a
// running engine (e.g. `etape -replay 2026-01-02 -speed 0 -replay-hold`):
//   tsx mock-engine/capture.ts session-e2e
// It subscribes to every topic in the wire contract, records the first snapshot
// per (topic,key) and a bounded set of subsequent deltas, then writes
// ui/fixtures/<name>.json.
import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import WebSocket from "ws";

const here = dirname(fileURLToPath(import.meta.url));
const name = process.argv[2] ?? "session-e2e";
const url = process.env.ENGINE_WS ?? "ws://127.0.0.1:8686/ws";

// Full Topic union from gen/wsmsg.ts (16 topics).
const TOPICS = [
  "md.quote", "md.book", "md.tape", "md.bars", "md.indicator",
  "scanner.rank", "scanner.hit", "news.item",
  "exec.account", "exec.positions", "exec.orders", "exec.fills", "exec.status",
  "sys.health", "sys.events", "config",
];

const snapshots: Array<{ topic: string; key?: string; payload: unknown }> = [];
const deltas: Array<{ afterMs: number; topic: string; key?: string; payload: unknown }> = [];
const seenSnap = new Set<string>();
const t0 = Date.now();

// The engine only sets a wire-level `key` for a few topics (indicator,
// scanner.rank, exec.account) — see engine/internal/uihub/mirror.go's
// snapshotFrames. For per-symbol market-data topics (md.quote/md.book/md.tape/
// md.bars) the discriminator lives inside the payload instead: a single object
// with a `symbol` field, or an array of such objects (tape ticks, bars — bars
// also carry `timeframe`, so multiple timeframes per symbol stay distinct).
// Falling back to `m.key` alone would collapse every symbol on those topics
// into one captured snapshot; derive an effective key from the payload so each
// symbol (and symbol+timeframe for bars) is captured independently.
function effectiveKey(payload: unknown): string | undefined {
  const item = Array.isArray(payload) ? payload[0] : payload;
  if (item && typeof item === "object" && "symbol" in (item as Record<string, unknown>)) {
    const rec = item as Record<string, unknown>;
    return typeof rec.timeframe === "string" ? `${rec.symbol}:${rec.timeframe}` : String(rec.symbol);
  }
  return undefined;
}

const ws = new WebSocket(url);
ws.on("open", () => {
  for (const topic of TOPICS) ws.send(JSON.stringify({ kind: "subscribe", topic }));
  setTimeout(finish, 3000); // capture window
});
ws.on("message", (raw) => {
  const m = JSON.parse(raw.toString()) as { kind: string; topic?: string; key?: string; payload?: unknown };
  if (!m.topic) return;
  const id = `${m.topic}#${m.key ?? effectiveKey(m.payload) ?? ""}`;
  if (m.kind === "snapshot" && !seenSnap.has(id)) {
    seenSnap.add(id);
    snapshots.push({ topic: m.topic, ...(m.key ? { key: m.key } : {}), payload: m.payload });
  } else if (m.kind === "delta" && deltas.length < 200) {
    deltas.push({ afterMs: Math.max(0, Date.now() - t0), topic: m.topic, ...(m.key ? { key: m.key } : {}), payload: m.payload });
  }
});
ws.on("error", (e) => { console.error("capture: ws error", e); process.exit(1); });

function finish() {
  const path = join(here, "..", "fixtures", `${name}.json`);
  writeFileSync(path, JSON.stringify({ snapshots, deltas }, null, 2) + "\n");
  console.log(`capture: wrote ${path} (${snapshots.length} snapshots, ${deltas.length} deltas)`);
  ws.close();
  process.exit(0);
}
