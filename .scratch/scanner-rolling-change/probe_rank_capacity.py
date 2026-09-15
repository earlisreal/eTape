"""Read-only live probe: 100/session ranks, OTC filtering and merged snapshots.

Run with the installed moomoo SDK and a logged-in OpenD on localhost:11111.
Creates no subscriptions, requests no history and performs no trading calls.
Writes only compact market-data evidence beside this script.
"""
import json
import time
from datetime import datetime, timezone
from pathlib import Path

from moomoo import ExchType, Market, OpenQuoteContext, RankSortDir, RET_OK


def main():
    report = {"utc": datetime.now(timezone.utc).isoformat(), "calls": []}
    ctx = OpenQuoteContext(host="127.0.0.1", port=11111)

    def call(label, method, **kwargs):
        started = time.perf_counter()
        ret, data = method(**kwargs)
        entry = {"label": label, "seconds": round(time.perf_counter() - started, 3), "ok": ret == RET_OK}
        report["calls"].append(entry)
        if ret != RET_OK:
            entry["error"] = str(data)
            raise RuntimeError(f"{label}: {data}")
        time.sleep(0.6)
        return data, entry

    try:
        endpoints = {
            "after": ctx.get_us_after_hours_rank,
            "overnight": ctx.get_us_overnight_rank,
            "pre": ctx.get_us_pre_market_rank,
            "rth": ctx.get_top_movers_rank,
        }
        ranks = {}
        for direction, sort_dir in (("gainers", RankSortDir.DESCENDING),
                                    ("losers", RankSortDir.ASCENDING)):
            for session, method in endpoints.items():
                args = {"count": 100, "sort_dir": sort_dir}
                if session == "rth":
                    args["market"] = Market.US
                (total, frame), entry = call(f"rank/{direction}/{session}", method, **args)
                codes = frame["security"].tolist()
                assert len(codes) == len(set(codes)) == 100, entry
                assert all(code.startswith("US.") for code in codes), entry
                ranks[direction, session] = set(codes)
                entry.update(rows=len(codes), total=int(total))
        all_codes = sorted(set().union(*ranks.values()))
        otc = set()
        for start in range(0, len(all_codes), 400):
            codes = all_codes[start:start + 400]
            frame, entry = call("static", ctx.get_stock_basicinfo, market=Market.US, code_list=codes)
            assert set(frame["code"]) == set(codes), "Static response omitted symbols"
            otc.update(frame.loc[frame["exchange_type"] == ExchType.US_PINK, "code"])
            entry.update(requested=len(codes), rows=len(frame))
        report.update(unique_candidates=len(all_codes), excluded_otc=len(otc))
        scenarios = {}
        for direction in ("gainers", "losers"):
            for label, sessions in (("premarket_startup", ("after", "overnight", "pre")),
                                    ("four_session_capacity_only", tuple(endpoints))):
                raw = set().union(*(ranks[direction, session] for session in sessions))
                scenarios[f"{direction}/{label}"] = sorted(raw - otc)
        snapshots = {}
        for label, codes in scenarios.items():
            assert 0 < len(codes) <= 400, "Merged request exceeds one snapshot batch"
            frame, entry = call(f"snapshot/{label}", ctx.get_market_snapshot, code_list=codes)
            assert set(frame["code"]) == set(codes), "Snapshot response omitted symbols"
            positive = frame["pre_price"].notna() & (frame["pre_price"] > 0)
            entry.update(requested=len(codes), rows=len(frame),
                         positive_pre_price=int(positive.sum()),
                         no_pre_price=frame.loc[~positive, "code"].tolist(),
                         update_time_min=str(frame["update_time"].min()),
                         update_time_max=str(frame["update_time"].max()))
            snapshots[label] = frame.set_index("code")
        label = "gainers/premarket_startup"
        for repeat in range(2):
            time.sleep(2)
            frame, entry = call(f"snapshot/repeat/{repeat + 1}", ctx.get_market_snapshot,
                                code_list=scenarios[label])
            assert set(frame["code"]) == set(scenarios[label]), "Repeat omitted symbols"
            current = frame.set_index("code")
            previous = snapshots[label]
            comparable = current["pre_price"].notna() & previous["pre_price"].notna()
            entry.update(rows=len(frame), changed_pre_prices=int(
                (comparable & (current["pre_price"] != previous["pre_price"])).sum()))
            snapshots[label] = current
        report["completed"] = True
    finally:
        ctx.close()
        path = Path(__file__).with_name("capacity-probe.json")
        path.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8", newline="\n")
        print(json.dumps(report, indent=2), flush=True)


if __name__ == "__main__":
    main()
