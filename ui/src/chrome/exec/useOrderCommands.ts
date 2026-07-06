import { useMemo } from "react";
import { OrderCommands, type CommandAdapter } from "./commands";
import type { ExecStore } from "../../data/ExecStore";
import type { ToastApi } from "../Toast";
import { soundEngine, type SoundApi } from "../../sound/SoundEngine";

export function useOrderCommands(
  cmd: CommandAdapter,
  exec: ExecStore,
  toast: ToastApi,
  now: () => number = () => Date.now(),
  sound: SoundApi = soundEngine,
): OrderCommands {
  return useMemo(() => new OrderCommands({ cmd, exec, toast, now, sound }), [cmd, exec, toast, now, sound]);
}
