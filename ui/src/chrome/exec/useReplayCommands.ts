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
    // Task 5 (U3): the synthetic-demo-market entry point — no knobs, just a
    // StartDemo command with empty args (mirrors GoLiveArgs/StartDemoArgs
    // both being intentionally-empty types on the engine side).
    startDemo: (): Promise<AckMsg> => cmd.sendCommand("StartDemo", {}),
  }), [cmd]);
}
