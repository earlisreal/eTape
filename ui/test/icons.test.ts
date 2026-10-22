import { readFileSync } from "node:fs";
import { PNG } from "pngjs";
import { expect, it } from "vitest";

it.each([16, 20, 24, 32, 40, 48, 64, 192, 512])(
  "keeps five separate bar rows in the transparent %ipx app icon",
  (size) => {
    const name = `${size < 192 ? "favicon" : "icon"}-${size}.png`;
    const image = PNG.sync.read(readFileSync(new URL(`../public/${name}`, import.meta.url)));
    expect([image.width, image.height]).toEqual([size, size]);
    expect(image.data[3]).toBe(0);

    const occupiedRows = Array.from({ length: size }, (_, y) => {
      for (let x = 0; x < size; x++) {
        if (image.data[(y * size + x) * 4 + 3] > 0) return true;
      }
      return false;
    });
    expect(occupiedRows.filter((occupied, y) => occupied && !occupiedRows[y - 1])).toHaveLength(5);
  },
);
