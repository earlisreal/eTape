import { EngineService } from "../gen/wails/github.com/earlisreal/eTape/engine/internal/uiapi/index.js";
import type * as Generated from "../gen/wails/github.com/earlisreal/eTape/engine/internal/uiapi/models.js";
import type { AckMsg } from "./contract";

export type MutationStatus = "accepted" | "blocked";

export type MutationResult = { status: MutationStatus; reason: string; revision: number };
export type ScannerFilters = Omit<Generated.ScannerFilters, "mode" | "floatUnit" | "volumeUnit"> & {
  mode: "gainers" | "losers" | "most_active";
  floatUnit: "K" | "M";
  volumeUnit: "K" | "M";
};
export type ScannerFiltersView = { filters: ScannerFilters; revision: number };
export type ScannerFiltersMutationResult = MutationResult & { filters: ScannerFilters };
export type WatchlistMutationResult = MutationResult & { symbols?: string[] };
export type Gate = { global: Generated.GlobalLimitsView; venue: { [key: string]: Generated.GateLimitsView } };
export type VenueConfig = { venues: Generated.Venue[]; gate: Gate };
export type VenueSetup = { file: VenueConfig; running: VenueConfig; credKeys: string[]; seed: Generated.SeedView; revision: number };
export type TestConnectionResult = {
  status: MutationStatus; reason: string; ok: boolean; env: string; accountId: string; accountType: string;
  message: string; accounts: Generated.TestAccount[];
};

export interface MutationClient {
  GetScannerFilters(): Promise<ScannerFiltersView>;
  SetScannerFilters(args: Generated.SetScannerFiltersArgs): Promise<ScannerFiltersMutationResult>;
  WatchlistAdd(args: Generated.WatchlistMutationArgs): Promise<WatchlistMutationResult>;
  WatchlistRemove(args: Generated.WatchlistMutationArgs): Promise<WatchlistMutationResult>;
  GetVenueSetup(): Promise<VenueSetup>;
  SetVenueSetup(args: Generated.SetVenueSetupArgs): Promise<MutationResult>;
  PutCredential(args: Generated.PutCredentialArgs): Promise<MutationResult>;
  DeleteCredential(args: Generated.DeleteCredentialArgs): Promise<MutationResult>;
  TestConnection(args: Generated.TestConnectionArgs): Promise<TestConnectionResult>;
}

export type LegacyMutation = (name: string, args: unknown) => Promise<AckMsg>;

export function makeMutationClient(useWails: boolean, _sendCommand: LegacyMutation): MutationClient {
  return useWails ? wailsMutations() : unavailableMutations();
}

export function mutationClient(commands: { mutations?: MutationClient; sendCommand: LegacyMutation }): MutationClient {
  return commands.mutations ?? legacyMutations(commands.sendCommand);
}

function unavailableMutations(): MutationClient {
  const unavailable = (): Promise<never> => Promise.reject(new Error("typed mutations require the native Wails host"));
  return {
    GetScannerFilters: unavailable,
    SetScannerFilters: unavailable,
    WatchlistAdd: unavailable,
    WatchlistRemove: unavailable,
    GetVenueSetup: unavailable,
    SetVenueSetup: unavailable,
    PutCredential: unavailable,
    DeleteCredential: unavailable,
    TestConnection: unavailable,
  };
}

function wailsMutations(): MutationClient {
  return {
    GetScannerFilters: async () => {
      const view = await EngineService.GetScannerFilters();
      return { filters: decodeFilters(view.filters), revision: view.revision };
    },
    SetScannerFilters: async (args) => {
      const result = await EngineService.SetScannerFilters(args);
      return { status: wailsStatus(result.status), reason: result.reason, filters: decodeFilters(result.filters), revision: result.revision };
    },
    WatchlistAdd: async (args) => watchlistGenerated(await EngineService.WatchlistAdd(args)),
    WatchlistRemove: async (args) => watchlistGenerated(await EngineService.WatchlistRemove(args)),
    GetVenueSetup: async () => {
      const setup = await EngineService.GetVenueSetup();
      return {
        file: venueConfigGenerated(setup.file),
        running: venueConfigGenerated(setup.running),
        credKeys: setup.credKeys ?? [], seed: setup.seed, revision: setup.revision,
      };
    },
    SetVenueSetup: async (args) => mutationGenerated(await EngineService.SetVenueSetup(args)),
    PutCredential: async (args) => mutationGenerated(await EngineService.PutCredential(args)),
    DeleteCredential: async (args) => mutationGenerated(await EngineService.DeleteCredential(args)),
    TestConnection: async (args) => {
      const result = await EngineService.TestConnection(args);
      return {
        status: wailsStatus(result.status), reason: result.reason, ok: result.ok, env: result.env,
        accountId: result.accountId, accountType: result.accountType, message: result.message, accounts: result.accounts ?? [],
      };
    },
  };
}

// Direct component tests still pass command-only fixtures. The native App
// always supplies `mutations`; browser construction above fails closed so
// migrated credential/mutation payloads never fall back to the generic socket.
function legacyMutations(sendCommand: LegacyMutation): MutationClient {
  return {
    GetScannerFilters: () => sendCommand("GetScannerFilters", {}).then((ack) => {
      requireAccepted(ack);
      const raw = record(ack.value, "GetScannerFilters");
      const filters = isRecord(raw.filters) ? decodeFilters(raw.filters) : decodeFilters(raw);
      return { filters, revision: integer(raw.revision) };
    }),
    SetScannerFilters: (args) => sendCommand("SetScannerFilters", args).then((ack) => {
      const raw = isRecord(ack.value) ? ack.value : {};
      return {
        status: status(ack), reason: ack.reason ?? "",
        filters: isRecord(raw.filters) ? decodeFilters(raw.filters) : decodeFilters(ack.value ?? args.filters),
        revision: integer(raw.revision),
      };
    }),
    WatchlistAdd: (args) => sendCommand("WatchlistAdd", args).then((ack) => watchlistResult(ack)),
    WatchlistRemove: (args) => sendCommand("WatchlistRemove", args).then((ack) => watchlistResult(ack)),
    GetVenueSetup: () => sendCommand("GetVenueSetup", {}).then((ack) => {
      requireAccepted(ack);
      const raw = record(ack.value, "GetVenueSetup");
      return { ...(raw as unknown as VenueSetup), revision: integer(raw.revision) };
    }),
    SetVenueSetup: (args) => sendCommand("SetVenueSetup", args).then((ack) => outcome(ack)),
    PutCredential: (args) => sendCommand("PutCredential", args).then((ack) => outcome(ack)),
    DeleteCredential: (args) => sendCommand("DeleteCredential", args).then((ack) => outcome(ack)),
    TestConnection: (args) => sendCommand("TestConnection", args).then((ack) => {
      const raw = isRecord(ack.value) ? ack.value : {};
      return {
        status: status(ack), reason: ack.reason ?? "",
        ok: raw.ok === true, env: text(raw.env), accountId: text(raw.accountId), accountType: text(raw.accountType),
        message: text(raw.message), accounts: list(raw.accounts).map((item) => {
          const account = record(item, "TestConnection account");
          return { accountId: text(account.accountId), accountType: text(account.accountType), env: text(account.env) };
        }),
      };
    }),
  };
}

function outcome(ack: AckMsg): MutationResult {
  const raw = isRecord(ack.value) ? ack.value : {};
  return { status: status(ack), reason: ack.reason ?? "", revision: integer(raw.revision) };
}

function watchlistResult(ack: AckMsg): WatchlistMutationResult {
  const raw = isRecord(ack.value) ? ack.value : {};
  const result: WatchlistMutationResult = {
    status: status(ack), reason: ack.reason ?? "",
    revision: integer(raw.revision),
  };
  if (Array.isArray(raw.symbols)) result.symbols = raw.symbols.filter((item): item is string => typeof item === "string");
  return result;
}

function requireAccepted(ack: AckMsg): void {
  if (ack.status !== "accepted") throw new Error(ack.reason ?? "mutation blocked");
}

function status(ack: AckMsg): MutationStatus {
  return ack.status === "accepted" ? "accepted" : "blocked";
}

function wailsStatus(value: Generated.MutationStatus): MutationStatus {
  return String(value) === "accepted" ? "accepted" : "blocked";
}

function mutationGenerated(result: Generated.MutationResult): MutationResult {
  return { status: wailsStatus(result.status), reason: result.reason, revision: result.revision };
}

function watchlistGenerated(result: Generated.WatchlistMutationResult): WatchlistMutationResult {
  return { status: wailsStatus(result.status), reason: result.reason, symbols: result.symbols ?? [], revision: result.revision };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function record(value: unknown, label: string): Record<string, unknown> {
  if (!isRecord(value)) throw new Error(`${label} response was not an object`);
  return value;
}

function text(value: unknown): string { return typeof value === "string" ? value : ""; }
function integer(value: unknown): number { return typeof value === "number" && Number.isFinite(value) ? value : 0; }
function list(value: unknown): unknown[] { return Array.isArray(value) ? value : []; }

function decodeFilters(value: unknown): ScannerFilters {
  const raw = isRecord(value) ? value : {};
  return {
    mode: raw.mode === "losers" || raw.mode === "most_active" ? raw.mode : "gainers",
    minChangePct: number(raw.minChangePct), maxFloatShares: typeof raw.maxFloatShares === "number" ? raw.maxFloatShares : null,
    minVolume: number(raw.minVolume), minVolumeRatio: number(raw.minVolumeRatio),
    floatUnit: raw.floatUnit === "K" ? "K" : "M", volumeUnit: raw.volumeUnit === "M" ? "M" : "K",
  };
}

function number(value: unknown): number { return typeof value === "number" && Number.isFinite(value) ? value : 0; }

function venueConfigGenerated(value: Generated.VenueConfig): VenueConfig {
  const venue: { [key: string]: Generated.GateLimitsView } = {};
  for (const [id, limits] of Object.entries(value.gate.venue ?? {})) {
    if (limits) venue[id] = limits;
  }
  return { venues: value.venues ?? [], gate: { global: value.gate.global, venue } };
}
