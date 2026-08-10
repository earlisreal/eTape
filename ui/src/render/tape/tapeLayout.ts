export const TAPE_PAD = 6;
export const TAPE_PRICE_WIDTH = 66;
export const TAPE_SIZE_WIDTH = 66;
export const TAPE_TIME_WIDTH = 56;
export const TAPE_MIN_GAP = 4;

export const TAPE_MIN_WIDTH = TAPE_PAD * 2 + TAPE_PRICE_WIDTH + TAPE_MIN_GAP + TAPE_SIZE_WIDTH;
export const TAPE_TIME_VISIBLE_MIN_WIDTH = TAPE_MIN_WIDTH + TAPE_MIN_GAP + TAPE_TIME_WIDTH;

export interface TapeColumnLayout {
  showTime: boolean;
  priceLeft: number;
  priceRight: number;
  sizeLeft: number;
  sizeRight: number;
  timeLeft?: number;
  timeRight?: number;
  gap: number;
}

export function computeTapeColumnLayout(width: number): TapeColumnLayout {
  const safeWidth = Number.isFinite(width) ? Math.max(0, width) : 0;
  const priceLeft = TAPE_PAD;
  const priceRight = priceLeft + TAPE_PRICE_WIDTH;

  if (safeWidth >= TAPE_TIME_VISIBLE_MIN_WIDTH) {
    const free = safeWidth - TAPE_PAD * 2 - TAPE_PRICE_WIDTH - TAPE_SIZE_WIDTH - TAPE_TIME_WIDTH;
    const gap = Math.max(TAPE_MIN_GAP, free / 2);
    const sizeLeft = priceRight + gap;
    const sizeRight = sizeLeft + TAPE_SIZE_WIDTH;
    const timeLeft = sizeRight + gap;
    const timeRight = timeLeft + TAPE_TIME_WIDTH;
    return { showTime: true, priceLeft, priceRight, sizeLeft, sizeRight, timeLeft, timeRight, gap };
  }

  // Dockview normally prevents this branch from receiving less than TAPE_MIN_WIDTH.
  // Keep the geometry finite and non-overlapping if a serialized layout or an
  // external resize briefly violates that constraint.
  const sizeRight = Math.max(priceRight + TAPE_MIN_GAP, safeWidth - TAPE_PAD);
  const sizeLeft = Math.max(priceRight + TAPE_MIN_GAP, sizeRight - TAPE_SIZE_WIDTH);
  return { showTime: false, priceLeft, priceRight, sizeLeft, sizeRight, gap: sizeLeft - priceRight };
}
