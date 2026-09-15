"""Bounded, read-only premarket polling test. No engine settings are changed.

Requires installed moomoo SDK and local logged-in OpenD. Tests one fixed
same-cycle candidate set; active rank data refreshes every iteration.
Stops on the first API failure; no subscriptions, history or trading calls.
"""
import json
import math
import statistics
import time
from datetime import datetime, timezone
from pathlib import Path

from moomoo import ExchType, Market, OpenQuoteContext, RET_OK


def changes(previous, current):
    return sum(value != current[code] for code, value in previous.items()
               if value is not None and current.get(code) is not None)


def distribution(values):
    ordered = sorted(values)
    return {"median": round(statistics.median(ordered), 4),
            "p95": round(ordered[math.ceil(0.95 * len(ordered)) - 1], 4),
            "max": round(max(ordered), 4)}


def main():
    report = {"utc": datetime.now(timezone.utc).isoformat(), "stages": [],
              "errors": [], "scope": "SDK rank + fixed merged snapshots, not full engine loop"}
    ctx = OpenQuoteContext(host="127.0.0.1", port=11111)

    def call(method, **kwargs):
        start = time.perf_counter()
        ret, result = method(**kwargs)
        if ret != RET_OK:
            report["errors"].append(str(result))
            raise RuntimeError(result)
        return result, time.perf_counter() - start

    try:
        codes = set()
        for method in (ctx.get_us_after_hours_rank, ctx.get_us_overnight_rank,
                       ctx.get_us_pre_market_rank):
            (_, frame), _ = call(method, count=100)
            codes.update(frame["security"])
            time.sleep(0.6)
        frame, _ = call(ctx.get_stock_basicinfo, market=Market.US, code_list=sorted(codes))
        assert set(frame["code"]) == codes, "Static response omitted symbols"
        codes -= set(frame.loc[frame["exchange_type"] == ExchType.US_PINK, "code"])
        codes = sorted(codes)
        assert 0 < len(codes) <= 400
        report["snapshot_symbols"] = len(codes)
        for interval, count in ((2, 16), (1, 61)):
            starts, latencies, rank_latencies, snapshot_latencies = [], [], [], []
            prices, rankings = [], []
            for index in range(count):
                if starts:
                    time.sleep(max(0, starts[-1] + interval - time.perf_counter()))
                starts.append(time.perf_counter())
                (_, rank), rank_seconds = call(ctx.get_us_pre_market_rank, count=100)
                assert len(set(rank["security"])) == 100
                frame, snapshot_seconds = call(ctx.get_market_snapshot, code_list=codes)
                assert set(frame["code"]) == set(codes), "Snapshot omitted symbols"
                prices.append({row.code: float(row.pre_price) if row.pre_price > 0 else None
                               for row in frame.itertuples()})
                rankings.append(tuple(rank["security"]))
                rank_latencies.append(rank_seconds)
                snapshot_latencies.append(snapshot_seconds)
                latencies.append(time.perf_counter() - starts[-1])
                if index and index % 15 == 0:
                    print(f"{interval}s stage: {index + 1}/{count} polls passed", flush=True)
            deltas = [changes(a, b) for a, b in zip(prices, prices[1:])]
            stage = {"interval_seconds": interval, "polls": count,
                     "elapsed_seconds": round(starts[-1] - starts[0], 3),
                     "cycle_seconds": distribution(latencies),
                     "rank_seconds": distribution(rank_latencies),
                     "snapshot_seconds": distribution(snapshot_latencies),
                     "start_interval_seconds": distribution([b-a for a, b in zip(starts, starts[1:])]),
                     "overrun_cycles": sum(value > interval for value in latencies),
                     "refreshes_with_price_changes": sum(value > 0 for value in deltas),
                     "symbol_price_transitions": sum(deltas),
                     "rank_order_or_membership_changes": sum(a != b for a, b in zip(rankings, rankings[1:])),
                     "missing_pre_price_last": sum(value is None for value in prices[-1].values())}
            if interval == 1:
                stage["same_stream_2s_price_transitions"] = sum(
                    changes(prices[i-2], prices[i]) for i in range(2, count, 2))
                stage["intermediate_1s_changed_prices"] = sum(
                    changes(prices[i-1], prices[i]) for i in range(1, count-1, 2))
                stage["intermediate_changes_reverted_by_2s"] = sum(
                    a is not None and prices[i].get(code) is not None
                    and prices[i][code] != a and prices[i+1].get(code) == a
                    for i in range(1, count-1, 2) for code, a in prices[i-1].items())
            report["stages"].append(stage)
            print(json.dumps(stage), flush=True)
        report["completed"] = True
    finally:
        ctx.close()
        Path(__file__).with_name("polling-probe.json").write_text(
            json.dumps(report, indent=2) + "\n", encoding="utf-8", newline="\n")


if __name__ == "__main__":
    assert changes({"A": 1, "B": None}, {"A": 2, "B": 3}) == 1
    assert distribution([1, 2, 3])["p95"] == 3
    main()
