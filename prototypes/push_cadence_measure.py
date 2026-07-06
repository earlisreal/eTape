#!/usr/bin/env python3
"""Steady-state push-cadence measurement (Monday checklist §5) — run during RTH.

Subscribes QUOTE + ORDER_BOOK + TICKER (+K_1M) on a mixed watchlist (mega-caps
+ current top gappers from the pre-market/intraday rank), counts pushes and
payload sizes over a window, and reports per-(symbol, subtype):

  pushes/sec, inter-push interval stats, ticks-per-push batch size, and a
  journal-volume extrapolation (events/sec x avg JSON payload x 16h session)

Feeds: UI coalescing default (30 Hz), OpenD push-frequency setting, journal
MB/day estimate. Quota: 4 subtypes x N symbols slots, released after the run
(>=60 s minimum hold). Usage: python3 push_cadence_measure.py [--secs 120]
[--symbols US.AAPL,US.TSLA,...] [--gappers 3]
"""

import argparse
import json
import statistics
import threading
import time
from collections import defaultdict
from datetime import datetime
from pathlib import Path

from moomoo import (CurKlineHandlerBase, OpenQuoteContext, OrderBookHandlerBase, RET_OK,
                    StockQuoteHandlerBase, SubType, TickerHandlerBase)

CAPTURE_DIR = Path(__file__).parent / 'captures'
MEGACAPS = ['US.AAPL', 'US.TSLA', 'US.NVDA']

lock = threading.Lock()
events = defaultdict(list)      # (code, subtype) -> [(t, payload_bytes, rows)]


def record(subtype, code, payload_rows, payload_bytes):
    with lock:
        events[(code, subtype)].append((time.monotonic(), payload_bytes, payload_rows))


def make_handlers():
    class Quote(StockQuoteHandlerBase):
        def on_recv_rsp(self, rsp_pb):
            ret, data = super().on_recv_rsp(rsp_pb)
            if ret == RET_OK and len(data):
                for rec in data.to_dict('records'):
                    record('QUOTE', rec['code'], 1, len(json.dumps(rec, default=str)))
            return ret, data

    class Book(OrderBookHandlerBase):
        def on_recv_rsp(self, rsp_pb):
            ret, data = super().on_recv_rsp(rsp_pb)
            if ret == RET_OK and isinstance(data, dict):
                record('ORDER_BOOK', data['code'], 1, len(json.dumps(data, default=str)))
            return ret, data

    class Tick(TickerHandlerBase):
        def on_recv_rsp(self, rsp_pb):
            ret, data = super().on_recv_rsp(rsp_pb)
            if ret == RET_OK and len(data):
                by_code = defaultdict(int)
                for rec in data.to_dict('records'):
                    by_code[rec['code']] += 1
                blob = len(json.dumps(data.to_dict('records'), default=str))
                for code, n in by_code.items():
                    record('TICKER', code, n, blob * n // max(1, len(data)))
            return ret, data

    class Kline(CurKlineHandlerBase):
        def on_recv_rsp(self, rsp_pb):
            ret, data = super().on_recv_rsp(rsp_pb)
            if ret == RET_OK and len(data):
                for rec in data.to_dict('records'):
                    record('K_1M', rec['code'], 1, len(json.dumps(rec, default=str)))
            return ret, data

    return Quote(), Book(), Tick(), Kline()


def top_gappers(ctx, n):
    try:
        ret, data = ctx.get_us_pre_market_rank(count=min(35, max(10, n * 3)))
        if ret != RET_OK:
            return []
        _, df = data
        out = []
        for r in df.to_dict('records'):
            code = r.get('security') or r.get('code')
            vol = r.get('pre_market_volume') or 0
            if code and vol >= 500_000:          # the volume floor, as designed
                out.append(code)
            if len(out) >= n:
                break
        return out
    except Exception:  # noqa: BLE001
        return []


def stats_line(label, samples):
    if len(samples) < 2:
        return f'{label:<40} n={len(samples)} (too few)'
    s = sorted(samples)
    return (f'{label:<40} n={len(s):<5} med={statistics.median(s):7.1f}  '
            f'p95={s[max(0, int(len(s) * .95) - 1)]:7.1f}  max={s[-1]:7.1f}')


def main(secs, symbols, n_gappers):
    ctx = OpenQuoteContext(host='127.0.0.1', port=11111)
    for h in make_handlers():
        ctx.set_handler(h)
    try:
        watch = list(symbols)
        gappers = top_gappers(ctx, n_gappers)
        watch += [g for g in gappers if g not in watch]
        print(f'watchlist: {watch} (gappers: {gappers})')

        subtypes = [SubType.QUOTE, SubType.ORDER_BOOK, SubType.TICKER, SubType.K_1M]
        ret, err = ctx.subscribe(watch, subtypes, is_first_push=False, extended_time=True)
        if ret != RET_OK:
            print(f'subscribe failed: {err}')
            return
        print(f'subscribed {len(watch)}x{len(subtypes)} = {len(watch) * len(subtypes)} slots; '
              f'measuring {secs}s...')
        t_start = time.monotonic()
        time.sleep(secs)
        window = time.monotonic() - t_start

        print(f'\n=== push cadence over {window:.0f}s ===')
        total_bytes_s, total_events_s = 0.0, 0.0
        summary = {}
        for (code, subtype), evs in sorted(events.items()):
            ts = [t for t, _, _ in evs if t >= t_start]
            if not ts:
                continue
            gaps_ms = [(b - a) * 1000 for a, b in zip(ts, ts[1:])]
            rate = len(ts) / window
            rows = sum(r for t, _, r in evs if t >= t_start)
            byts = sum(b for t, b, _ in evs if t >= t_start)
            total_bytes_s += byts / window
            total_events_s += rate
            summary[f'{code}/{subtype}'] = {
                'pushes_per_s': round(rate, 2), 'rows': rows,
                'bytes_per_s': round(byts / window, 1),
                'gap_ms': {'median': round(statistics.median(gaps_ms), 1) if gaps_ms else None,
                           'min': round(min(gaps_ms), 1) if gaps_ms else None}}
            print(stats_line(f'{code} {subtype} ({rate:5.2f}/s, {rows} rows)', gaps_ms))

        est_day_mb = total_bytes_s * 16 * 3600 / 1e6   # 04:00-20:00 session
        print(f'\ntotals: {total_events_s:.1f} events/s, {total_bytes_s / 1024:.1f} KiB/s JSON')
        print(f'journal extrapolation (this watchlist, 16h 04:00-20:00): ~{est_day_mb:.0f} MB/day')
        print('(RTH-only 6.5h: ~%.0f MB)' % (total_bytes_s * 6.5 * 3600 / 1e6))

        out = CAPTURE_DIR / f'push_cadence_{datetime.now():%Y%m%d_%H%M%S}.json'
        out.write_text(json.dumps({'watchlist': watch, 'window_s': round(window, 1),
                                   'summary': summary,
                                   'est_16h_mb': round(est_day_mb, 1)}, indent=1))
        print(f'raw -> {out}')

        hold = 61 - (time.monotonic() - t_start)
        if hold > 0:
            print(f'holding {hold:.0f}s for the 1-min unsubscribe minimum...')
            time.sleep(hold)
        ctx.unsubscribe_all()
    finally:
        ctx.close()


if __name__ == '__main__':
    ap = argparse.ArgumentParser()
    ap.add_argument('--secs', type=int, default=120)
    ap.add_argument('--symbols', default=','.join(MEGACAPS))
    ap.add_argument('--gappers', type=int, default=3)
    args = ap.parse_args()
    main(args.secs, [s.strip() for s in args.symbols.split(',') if s.strip()], args.gappers)
