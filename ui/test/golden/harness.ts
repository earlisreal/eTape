// Golden-image harness — the shape of wickplot's CanvasChartSampleRenderTest
// (fixture states → render the real painter offscreen at 2× → PNGs to a
// samples dir for eyeballing), upgraded from its size-only assertion to a
// strict pixel-diff against checked-in goldens.
import { createCanvas, registerFont } from "canvas";
import { PNG } from "pngjs";
import pixelmatch from "pixelmatch";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const GOLDEN_DIR = join(here, "goldens");
const OUTPUT_DIR = join(here, "__output__");
const SCALE = 2; // render at 2× like wickplot's harness; goldens are HiDPI

// Register the app's real mono face before any canvas exists so text metrics
// are deterministic on every machine — node-canvas resolves the quoted
// "IBM Plex Mono" in FONTS.mono against this registration.
registerFont(join(here, "fonts", "IBMPlexMono-Regular.ttf"), { family: "IBM Plex Mono" });

/** Render a painter offscreen at 2× in CSS-pixel coordinates and decode to PNG. */
export function renderScene(
  width: number,
  height: number,
  paint: (ctx: CanvasRenderingContext2D) => void,
): PNG {
  const canvas = createCanvas(width * SCALE, height * SCALE);
  const ctx = canvas.getContext("2d") as unknown as CanvasRenderingContext2D;
  ctx.scale(SCALE, SCALE);
  paint(ctx);
  return PNG.sync.read(canvas.toBuffer("image/png"));
}

/**
 * Strict pixel-diff against the checked-in golden. Every run writes the
 * current render to __output__/ for eyeballing; failures also write a diff
 * image. UPDATE_GOLDENS=1 (npm run test:golden:update) rewrites the golden
 * instead of asserting — review __output__/ before committing the result.
 */
export function expectGolden(name: string, png: PNG): void {
  mkdirSync(OUTPUT_DIR, { recursive: true });
  const rendered = PNG.sync.write(png);
  writeFileSync(join(OUTPUT_DIR, `${name}.png`), rendered);

  const goldenPath = join(GOLDEN_DIR, `${name}.png`);
  if (process.env.UPDATE_GOLDENS === "1") {
    mkdirSync(GOLDEN_DIR, { recursive: true });
    writeFileSync(goldenPath, rendered);
    return;
  }
  if (!existsSync(goldenPath)) {
    throw new Error(
      `golden missing: ${name}.png — run "npm run test:golden:update", eyeball __output__/${name}.png, commit goldens/${name}.png`,
    );
  }
  const golden = PNG.sync.read(readFileSync(goldenPath));
  if (golden.width !== png.width || golden.height !== png.height) {
    throw new Error(
      `golden size mismatch for ${name}: golden ${golden.width}×${golden.height}, rendered ${png.width}×${png.height}`,
    );
  }
  const diff = new PNG({ width: png.width, height: png.height });
  const differing = pixelmatch(golden.data, png.data, diff.data, png.width, png.height, { threshold: 0.05 });
  if (differing > 0) {
    writeFileSync(join(OUTPUT_DIR, `${name}.diff.png`), PNG.sync.write(diff));
    throw new Error(`golden mismatch for ${name}: ${differing} differing pixels — see __output__/${name}.diff.png`);
  }
}
