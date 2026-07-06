import { useMemo } from "react";
import { OrderCommands, type CommandAdapter } from "./commands";
import type { ExecStore } from "../../data/ExecStore";
import type { ToastApi } from "../Toast";

export function useOrderCommands(cmd: CommandAdapter, exec: ExecStore, toast: ToastApi, now: () => number = () => Date.now()): OrderCommands {
  return useMemo(() => new OrderCommands({ cmd, exec, toast, now }), [cmd, exec, toast, now]);
}
