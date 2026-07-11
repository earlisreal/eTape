import { useMemo } from "react";
import type { AckMsg } from "../../wire/contract";

export interface ReplayCommandAdapter {
  sendCommand(name: string, args: unknown): Promise<AckMsg>;
  sendQuery(name: string, args: unknown): Promise<unknown>;
}

export function useReplayCommands(cmd: ReplayCommandAdapter) {
  return useMemo(() => ({
    listDays: async (): Promise<string[]> => ((await cmd.sendQuery("ListReplayDays", {})) as string[]) ?? [],
    start: (day: string, speed: number): Promise<AckMsg> => cmd.sendCommand("StartReplay", { day, speed }),
    goLive: (): Promise<AckMsg> => cmd.sendCommand("GoLive", {}),
  }), [cmd]);
}
