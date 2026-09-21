import { useEffect } from "react";
import type { Stores } from "../data/registry";
import { soundEngine, type SoundSink } from "./SoundEngine";

// Subscribes the imperative SoundEngine to fill/reject stores and resumes the
// AudioContext on the first user gesture. Scanner panels own Scanner hits.
export function useSoundWiring(stores: Stores, engine: SoundSink = soundEngine): void {
  useEffect(() => {
    const offFill = stores.fills.onNewFill((f) => engine.orderFilled(f.side, f.tsMs));
    const offReject = stores.exec.onOrderRejected(() => engine.orderRejected());

    const unlock = () => engine.unlock();
    window.addEventListener("pointerdown", unlock, { once: true, capture: true });
    window.addEventListener("keydown", unlock, { once: true, capture: true });

    return () => {
      offFill(); offReject();
      window.removeEventListener("pointerdown", unlock, true);
      window.removeEventListener("keydown", unlock, true);
    };
  }, [stores, engine]);
}
