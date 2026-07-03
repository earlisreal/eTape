#!/usr/bin/env python3
"""Supplement to moomoo_latency_bench.py — the two paths the first run missed:

  1. K_1M subscription (eTape's live 1m bar feed): subscribe() round-trip,
     first cached push, and get_cur_kline() cache read (potential intraday
     backfill source — report how many bars the sub cache actually holds)
  2. get_order_book() cache read (initial DOM paint / reconnect recovery),
     including depth levels returned (US LV3 = 10 expected, HK LV1 = 1)

Quota: 12 subscription slots (6 symbols x K_1M + ORDER_BOOK), released via
unsubscribe_all after the mandatory 60s. No historical quota consumed.
"""

import json
import statistics
import threading
import time
from datetime import datetime
from pathlib import Path

from moomoo import (
    CurKlineHandlerBase,
    KLType,
    OpenQuoteContext,
    OrderBookHandlerBase,
    RET_OK,
    SubType,
)

HOST, PORT = '127.0.0.1', 11111
SYMBOLS = ['US.AAPL', 'US.TSLA', 'US.NVDA', 'US.MSFT', 'HK.00700', 'HK.09988']
QUERY_ITERS = 20
CAPTURE_DIR = Path(__file__).parent / 'captures'

T0 = time.monotonic()
def ms() -> float:
    return (time.monotonic() - T0) * 1000


def timed(fn, *args, **kwargs):
    t = time.monotonic()
    result = fn(*args, **kwargs)
    return (time.monotonic() - t) * 1000, result


def stats(samples):
    s = sorted(samples)
    return {'n': len(s), 'min': s[0], 'median': statistics.median(s),
            'mean': statistics.fmean(s), 'p95': s[max(0, int(len(s) * 0.95) - 1)],
            'max': s[-1]}


def fmt(label, st):
    print(f'{label:<44} n={st["n"]:<3} min={st["min"]:6.1f}  med={st["median"]:6.1f}  '
          f'mean={st["mean"]:6.1f}  p95={st["p95"]:6.1f}  max={st["max"]:6.1f}')


first_push = {}
push_lock = threading.Lock()

def record_push(subtype, codes):
    t = ms()
    with push_lock:
        for code in codes:
            first_push.setdefault((code, subtype), t)


class KlineHandler(CurKlineHandlerBase):
    def on_recv_rsp(self, rsp_pb):
        ret, data = super().on_recv_rsp(rsp_pb)
        if ret == RET_OK and len(data):
            record_push('K_1M', set(data['code']))
        return ret, data


class OrderBookHandler(OrderBookHandlerBase):
    def on_recv_rsp(self, rsp_pb):
        ret, data = super().on_recv_rsp(rsp_pb)
        if ret == RET_OK:
            record_push('ORDER_BOOK', {data['code']})
        return ret, data


def main():
    results = {'started': datetime.now().astimezone().isoformat()}
    ctx = OpenQuoteContext(host=HOST, port=PORT)
    ctx.set_handler(KlineHandler())
    ctx.set_handler(OrderBookHandler())

    # ---- subscribe K_1M + ORDER_BOOK per symbol ----
    print('--- subscribe: per-symbol, K_1M+ORDER_BOOK in one call ---')
    sub_samples, sub_return_at = [], {}
    for sym in SYMBOLS:
        el, (ret, err) = timed(ctx.subscribe, [sym], [SubType.K_1M, SubType.ORDER_BOOK],
                               is_first_push=True, extended_time=True)
        sub_return_at[sym] = ms()
        print(f'{ms():7.0f}ms  subscribe {sym:<10}: {el:7.1f} ms  '
              f'[{"ok" if ret == RET_OK else f"ERR {err}"}]')
        if ret == RET_OK:
            sub_samples.append(el)
        time.sleep(0.1)
    results['subscribe_k1m_ob_ms'] = sub_samples

    # ---- first pushes ----
    expected = {(s, t) for s in SYMBOLS for t in ('K_1M', 'ORDER_BOOK')}
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        with push_lock:
            if expected <= set(first_push):
                break
        time.sleep(0.05)

    print('\n--- subscribe-return -> first push ---')
    push_deltas, by_type = {}, {}
    for (code, subtype), t in sorted(first_push.items()):
        d = round(t - sub_return_at[code], 1)
        push_deltas[f'{code}/{subtype}'] = d
        by_type.setdefault(subtype, []).append(d)
    for subtype, deltas in sorted(by_type.items()):
        fmt(f'first push {subtype}', stats(deltas))
    missing = sorted(expected - set(first_push))
    if missing:
        print(f'no first push seen for: {", ".join(f"{c}/{t}" for c, t in missing)}')
    results['first_push_delta_ms'] = push_deltas
    results['first_push_missing'] = [f'{c}/{t}' for c, t in missing]

    # ---- get_cur_kline (K_1M subscription cache read) ----
    print('\n--- get_cur_kline K_1M num=1000 (subscription cache read) ---')
    for sym in ['US.AAPL', 'HK.00700']:
        samples, rows = [], 0
        for _ in range(QUERY_ITERS):
            el, (ret, df) = timed(ctx.get_cur_kline, sym, 1000, KLType.K_1M)
            if ret == RET_OK:
                samples.append(el)
                rows = len(df)
            time.sleep(0.05)
        fmt(f'get_cur_kline {sym} ({rows} bars)', stats(samples))
        results[f'get_cur_kline_{sym}_ms'] = samples
        results[f'get_cur_kline_{sym}_bars'] = rows

    # ---- get_order_book (ORDER_BOOK subscription cache read) ----
    print('\n--- get_order_book num=10 (subscription cache read) ---')
    for sym in ['US.AAPL', 'HK.00700']:
        samples, levels = [], 0
        for _ in range(QUERY_ITERS):
            el, (ret, book) = timed(ctx.get_order_book, sym, num=10)
            if ret == RET_OK:
                samples.append(el)
                levels = max(len(book.get('Bid', [])), len(book.get('Ask', [])))
            time.sleep(0.05)
        fmt(f'get_order_book {sym} ({levels} levels)', stats(samples))
        results[f'get_order_book_{sym}_ms'] = samples
        results[f'get_order_book_{sym}_levels'] = levels

    # ---- teardown ----
    wait = 61 - (ms() - sub_return_at[SYMBOLS[0]]) / 1000
    if wait > 0:
        print(f'\nwaiting {wait:.0f}s to satisfy the 1-minute minimum before unsubscribing...')
        time.sleep(wait)
    el, (ret, err) = timed(ctx.unsubscribe_all)
    print(f'unsubscribe_all: {el:.1f} ms  [{"ok" if ret == RET_OK else f"ERR {err}"}]')
    ctx.close()

    CAPTURE_DIR.mkdir(exist_ok=True)
    out = CAPTURE_DIR / f'moomoo_latency_supplement_{datetime.now():%Y%m%d_%H%M%S}.json'
    out.write_text(json.dumps(results, indent=1))
    print(f'raw samples -> {out}')


if __name__ == '__main__':
    main()
