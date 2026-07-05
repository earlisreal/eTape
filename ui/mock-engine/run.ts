import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { startMockEngine, type Fixture } from "./server";

const here = dirname(fileURLToPath(import.meta.url));

// Fixture selection: defaults to "session-basic" (Plan 1's quote/health/events
// dev flow — unchanged so its docs/tests keep working). Select another fixture
// file from ui/fixtures/<name>.json via a CLI arg or the FIXTURE env var:
//   npm run mock-engine -- chart-session      (candles + VWAP dev-app fixture, Plan 2)
//   npm run mock-engine -- ladder-tape        (L2 book + tape + working orders, Plan 3)
//   npm run mock-engine -- monitoring         (scanner rank/hit + news, Plan 4)
//   FIXTURE=chart-session npm run mock-engine
const name = process.argv[2] ?? process.env.FIXTURE ?? "session-basic";
const fixture = JSON.parse(
  readFileSync(join(here, "..", "fixtures", `${name}.json`), "utf8"),
) as Fixture;

const port = 8686;
startMockEngine({ port, fixture });
console.log(`mock engine listening on ws://127.0.0.1:${port}/ws (fixture: ${name})`);
