import type { QueryVolumeProfileArgs, QueryVolumeProfileResult } from "../../gen/wsmsg";

export interface VolumeProfileProjection {
  status: string;
  result: QueryVolumeProfileResult | null;
  detail: string;
}

function selectionKey(a: QueryVolumeProfileArgs): string {
  return `${a.symbol}|${a.timeframe}|${a.fromMs}|${a.toMs}|${a.rows}|${a.valueArea}`;
}

function validResult(r: QueryVolumeProfileResult): boolean {
  if (r.source !== "captured" || typeof r.partial !== "boolean" || !Array.isArray(r.reasons) || !r.reasons.every((s) => typeof s === "string") || !Array.isArray(r.rows) || r.rows.length > 200 || !Number.isSafeInteger(r.capturedVolume) || r.capturedVolume < 0 || !Number.isSafeInteger(r.asOfMs) || r.asOfMs < 0) return false;
  if (!r.rows.every((row) => Number.isFinite(row.lower) && row.lower > 0 && Number.isFinite(row.upper) && row.upper >= row.lower && Number.isSafeInteger(row.volume) && row.volume >= 0)) return false;
  if (r.status === "ready") return r.rows.length > 0 && r.capturedVolume > 0 && r.rows.reduce((sum, row) => sum + row.volume, 0) === r.capturedVolume && [r.poc, r.vah, r.val].every((n) => typeof n === "number" && Number.isFinite(n) && n > 0);
  if (r.status === "empty") return r.rows.length === 0 && r.capturedVolume === 0 && r.poc === undefined && r.vah === undefined && r.val === undefined;
  return ["busy", "too_large", "unavailable", "invalid", "error"].includes(r.status);
}

export class VolumeProfileController {
  private selection: QueryVolumeProfileArgs | null = null;
  private key = "";
  private generation = 0;
  private inFlight = false;
  private pending = false;
  private disposed = false;
  private poll: ReturnType<typeof setInterval> | null = null;
  private coalesce: ReturnType<typeof setTimeout> | null = null;
  private projection: VolumeProfileProjection = { status: "idle", result: null, detail: "" };

  constructor(
    private readonly query: (selection: QueryVolumeProfileArgs) => Promise<QueryVolumeProfileResult>,
    private readonly connected: () => boolean,
    private readonly changed: (projection: VolumeProfileProjection) => void,
  ) {}

  snapshot(): VolumeProfileProjection { return this.projection; }

  setSelection(selection: QueryVolumeProfileArgs | null, unavailableReason = ""): void {
    if (this.disposed) return;
    const key = selection ? selectionKey(selection) : `none:${unavailableReason}`;
    if (key === this.key) return;
    this.key = key;
    this.selection = selection;
    this.generation++;
    if (this.poll !== null) clearInterval(this.poll);
    if (this.coalesce !== null) clearTimeout(this.coalesce);
    this.poll = this.coalesce = null;
    this.publish({ status: selection ? "loading" : unavailableReason ? "unavailable" : "idle", result: null, detail: unavailableReason });
    if (!selection) return;
    this.coalesce = setTimeout(() => { this.coalesce = null; void this.refresh(); }, 50);
    this.poll = setInterval(() => { void this.refresh(); }, 1000);
  }

  async refresh(): Promise<void> {
    if (this.disposed || !this.selection) return;
    if (this.inFlight) { this.pending = true; return; }
    if (!this.connected()) {
      this.publish({ ...this.projection, status: this.projection.result ? "stale" : "loading", detail: "Disconnected" });
      return;
    }
    const selection = this.selection, generation = this.generation;
    this.inFlight = true;
    this.pending = false;
    try {
      const result = await this.query(selection);
      if (this.disposed || generation !== this.generation) return;
      if (!result?.selection || selectionKey(result.selection) !== selectionKey(selection)) throw new Error("Profile selection mismatch");
      if (!validResult(result)) throw new Error("Invalid profile response");
      const detail = result.status === "too_large" ? "Range too large or timed out; zoom in"
        : result.reasons.join(", ").replaceAll("_", " ");
      if (result.status === "ready" || result.status === "empty") {
        this.publish({ status: result.status, result, detail });
      } else {
        this.publish({ status: this.projection.result ? "stale" : result.status, result: this.projection.result, detail });
      }
    } catch (error) {
      if (!this.disposed && generation === this.generation) {
        this.publish({ status: this.projection.result ? "stale" : "error", result: this.projection.result,
          detail: error instanceof Error ? error.message : "Profile query failed" });
      }
    } finally {
      this.inFlight = false;
      if (!this.disposed && this.selection && (this.pending || generation !== this.generation)) {
        this.pending = false;
        if (this.coalesce !== null) clearTimeout(this.coalesce);
        this.coalesce = setTimeout(() => { this.coalesce = null; void this.refresh(); }, 50);
      }
    }
  }

  dispose(): void {
    this.disposed = true;
    this.generation++;
    if (this.poll !== null) clearInterval(this.poll);
    if (this.coalesce !== null) clearTimeout(this.coalesce);
    this.poll = this.coalesce = null;
  }

  private publish(projection: VolumeProfileProjection): void { this.projection = projection; this.changed(projection); }
}
