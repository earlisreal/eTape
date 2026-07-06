#!/usr/bin/env python3
"""Pre-market gap-scanner verification (Monday checklist §1).

Measures, during live US pre-market (04:00-09:30 ET):
  1. Qot_GetUSPreMarketRank (3410) poll RTT and *effective refresh cadence* —
     per-symbol intervals between changes of (pre_market_price, pre_market_volume).
  2. Rank staleness vs ground truth — per poll, batch-snapshot the top-N ranked
     symbols (LV3 snapshot is real-time; pre-market volume is monotonic), then
     estimate lag = t_rank_poll - earliest snapshot time whose volume >= the
     rank row's volume. Median over all changed rows = scanner-visible latency.
  3. Tiny-print check — volumes of top-ranked rows (CLLS appeared on 1,831 sh
     on 2026-07-03; a mandatory client-side volume floor is assumed).

Rate budget: rank 1/s (limit 60/30s), snapshot 1/s batch of TOP_N (limit 60/30s).
Zero subscription quota (request APIs only).

Usage: python3 premarket_rank_latency.py [--secs 180] [--count 35] [--top 5]
"""

import argparse
import json
import statistics
import time
from bisect import bisect_left
from datetime import datetime
from pathlib import Path

from moomoo import OpenQuoteContext, RET_OK

HOST, PORT = '127.0.0.1', 11111
CAPTURE_DIR = Path(__file__).parent / 'captures'


def stats(samples):
    if not samples:
        return None
    s = sorted(samples)
    return {'n': len(s), 'min': round(s[0], 1), 'median': round(statistics.median(s), 1),
            'mean': round(statistics.fmean(s), 1),
            'p95': round(s[max(0, int(len(s) * 0.95) - 1)], 1), 'max': round(s[-1], 1)}


def fmt(label, st):
    if st is None:
        print(f'{label:<44} (no samples)')
        return
    print(f'{label:<44} n={st["n"]:<4} min={st["min"]:8.1f}  med={st["median"]:8.1f}  '
          f'mean={st["mean"]:8.1f}  p95={st["p95"]:8.1f}  max={st["max"]:8.1f}')


def code_of(row):
    return row.get('security') or row.get('code')


def main(secs, count, top_n):
    ctx = OpenQuoteContext(host=HOST, port=PORT)
    polls = []                 # raw rank polls (kept for offline analysis)
    rank_rtt = []
    snap_rtt = []
    last_seen = {}             # code -> (price, volume)
    change_intervals = {}      # code -> [secs between changes]
    last_change_t = {}         # code -> wall time of last observed change
    snap_series = {}           # code -> ([t...], [volume...]) monotonic volume series
    lag_samples = []           # rank-vs-snapshot staleness estimates (s)
    row_volumes = []           # (code, change_ratio, volume) every first sighting
    seen_rows = set()

    t_end = time.time() + secs
    n_poll = 0
    try:
        while time.time() < t_end:
            loop_start = time.time()

            # -- rank poll --------------------------------------------------
            t0 = time.monotonic()
            ret, data = ctx.get_us_pre_market_rank(count=count)
            rtt = (time.monotonic() - t0) * 1000
            t_rank = time.time()
            if ret != RET_OK:
                print(f'[{n_poll}] rank error: {data}')
                time.sleep(1.0)
                continue
            rank_rtt.append(rtt)
            all_count, df = data
            rows = df.to_dict('records')
            polls.append({'t': t_rank, 'rtt_ms': rtt, 'all_count': all_count,
                          'rows': [{k: (str(v) if not isinstance(v, (int, float, str, bool, type(None))) else v)
                                    for k, v in r.items()} for r in rows]})

            top_codes = []
            for r in rows:
                code = code_of(r)
                if code is None:
                    continue
                if len(top_codes) < top_n:
                    top_codes.append(code)
                price, vol = r.get('pre_market_price'), r.get('pre_market_volume')
                cr = r.get('pre_market_change_ratio')
                if code not in seen_rows:
                    seen_rows.add(code)
                    row_volumes.append((code, cr, vol))
                cur = (price, vol)
                if code in last_seen and cur != last_seen[code]:
                    if code in last_change_t:
                        change_intervals.setdefault(code, []).append(t_rank - last_change_t[code])
                    last_change_t[code] = t_rank
                    # staleness: earliest snapshot that had already reached this volume
                    ts, vs = snap_series.get(code, ([], []))
                    if vs and vol is not None:
                        i = bisect_left(vs, vol)
                        if i < len(vs):
                            lag_samples.append(t_rank - ts[i])
                elif code not in last_seen:
                    last_change_t[code] = t_rank
                last_seen[code] = cur

            # -- ground-truth snapshot of current top symbols ----------------
            if top_codes:
                t0 = time.monotonic()
                sret, sdata = ctx.get_market_snapshot(top_codes)
                snap_rtt.append((time.monotonic() - t0) * 1000)
                t_snap = time.time()
                if sret == RET_OK:
                    for sr in sdata.to_dict('records'):
                        code = sr.get('code')
                        vol = sr.get('pre_volume')
                        if code is None or vol is None:
                            continue
                        ts, vs = snap_series.setdefault(code, ([], []))
                        if not vs or vol > vs[-1]:
                            ts.append(t_snap)
                            vs.append(vol)

            n_poll += 1
            if n_poll % 15 == 0:
                changed = sum(len(v) for v in change_intervals.values())
                print(f'  poll {n_poll}: all_count={all_count}, tracked={len(last_seen)}, '
                      f'changes={changed}, lag_samples={len(lag_samples)}')
            time.sleep(max(0.0, 1.0 - (time.time() - loop_start)))
    finally:
        ctx.close()

    # ---- summary --------------------------------------------------------
    print('\n=== Qot_GetUSPreMarketRank refresh-latency summary ===')
    print(f'polls={n_poll} over {secs}s, rank all_count last={all_count}')
    fmt('rank poll RTT (ms)', stats(rank_rtt))
    fmt('snapshot batch RTT (ms)', stats(snap_rtt))
    all_intervals = [i for v in change_intervals.values() for i in v]
    fmt('per-symbol change interval (s)', stats(all_intervals))
    fmt('rank staleness vs snapshot (s)', stats(lag_samples))

    hot = sorted(change_intervals.items(), key=lambda kv: -len(kv[1]))[:8]
    print('\nMost active symbols (change count, median interval s):')
    for code, iv in hot:
        print(f'  {code:<12} changes={len(iv):<4} med_interval={statistics.median(iv):5.1f}s')

    print('\nTiny-print check — lowest pre-market volumes among ranked rows:')
    for code, cr, vol in sorted(row_volumes, key=lambda r: (r[2] is None, r[2]))[:10]:
        print(f'  {code:<12} vol={vol!s:>12}  pre_chg={cr}%')

    out = CAPTURE_DIR / f'premarket_rank_latency_{datetime.now().strftime("%Y%m%dT%H%M%S")}.json'
    out.write_text(json.dumps({
        'meta': {'secs': secs, 'count': count, 'top_n': top_n, 'polls': n_poll},
        'rank_rtt_ms': stats(rank_rtt), 'snap_rtt_ms': stats(snap_rtt),
        'change_intervals_s': {k: [round(x, 2) for x in v] for k, v in change_intervals.items()},
        'lag_samples_s': [round(x, 2) for x in lag_samples],
        'row_volumes': [[c, cr, v] for c, cr, v in row_volumes],
        'polls': polls,
    }, default=str))
    print(f'\nraw capture -> {out}')


if __name__ == '__main__':
    ap = argparse.ArgumentParser()
    ap.add_argument('--secs', type=int, default=180)
    ap.add_argument('--count', type=int, default=35)
    ap.add_argument('--top', type=int, default=5, dest='top_n')
    args = ap.parse_args()
    main(args.secs, args.count, args.top_n)
