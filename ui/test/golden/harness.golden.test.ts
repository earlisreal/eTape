import { describe, it } from "vitest";
import { renderScene, expectGolden } from "./harness";
import { getPalette, FONTS } from "../../src/render/palette";

describe("golden harness", () => {
  it("renders text + fills deterministically with the registered font", () => {
    const p = getPalette("light");
    const png = renderScene(200, 80, (ctx) => {
      ctx.fillStyle = p.bg;
      ctx.fillRect(0, 0, 200, 80);
      ctx.fillStyle = p.up;
      ctx.fillRect(10, 10, 40, 16);
      ctx.fillStyle = p.text;
      ctx.font = `12px ${FONTS.mono}`;
      ctx.textBaseline = "middle";
      ctx.fillText("eTape 3.50 × 1,428", 10, 52);
    });
    expectGolden("harness-smoke", png);
  });
});
