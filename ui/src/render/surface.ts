// A paintable surface polled by the scheduler. Backed by a PaintStore's dirty flag
// in practice; kept as a minimal interface so the render layer never imports data.
export interface Surface {
  id: string;
  isDirty(): boolean;
  paint(): void;
}

export interface RafLike {
  request(cb: () => void): number;
  cancel(id: number): void;
}

// Production RafLike over window.requestAnimationFrame.
export const browserRaf: RafLike = {
  request: (cb) => requestAnimationFrame(cb),
  cancel: (id) => cancelAnimationFrame(id),
};
