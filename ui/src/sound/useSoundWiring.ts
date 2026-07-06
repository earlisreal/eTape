import { useEffect } from "react";
import type { Stores } from "../data/registry";
import { soundEngine, type SoundSink } from "./SoundEngine";

// Subscribes the imperative SoundEngine to the three trigger stores and resumes the
// AudioContext on the first user gesture. Mount once (AppShell), never conditionally.
export function useSoundWiring(stores: Stores, engine: SoundSink = soundEngine): void {
  useEffect(() => {
    const offFill = stores.fills.onNewFill((f) => engine.orderFilled(f.side, f.tsMs));
    const offReject = stores.exec.onOrderRejected(() => engine.orderRejected());
    const offHit = stores.scanner.onNewHit(() => engine.scannerHit());

    const unlock = () => engine.unlock();
    window.addEventListener("pointerdown", unlock, { once: true, capture: true });
    window.addEventListener("keydown", unlock, { once: true, capture: true });

    return () => {
      offFill(); offReject(); offHit();
      window.removeEventListener("pointerdown", unlock, true);
      window.removeEventListener("keydown", unlock, true);
    };
  }, [stores, engine]);
}
