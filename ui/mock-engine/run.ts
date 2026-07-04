import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { startMockEngine, type Fixture } from "./server";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = JSON.parse(
  readFileSync(join(here, "..", "fixtures", "session-basic.json"), "utf8"),
) as Fixture;

const port = 8686;
startMockEngine({ port, fixture });
console.log(`mock engine listening on ws://127.0.0.1:${port}/ws`);
