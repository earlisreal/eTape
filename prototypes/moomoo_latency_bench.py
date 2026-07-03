#!/usr/bin/env python3
"""moomoo OpenD market-data latency benchmark (counterpart to tz_order_latency_bench.py).

Measures, in ms:
  1. OpenQuoteContext creation      — TCP connect + InitConnect handshake (x2 samples)
  2. subscribe() round-trip         — per-symbol QUOTE+ORDER_BOOK+TICKER (6 cold samples)
                                      and one batch watchlist call (5 symbols x TICKER)
  3. subscribe -> first push        — per subtype, via the cached first-push path
  4. get_stock_quote()              — x20 single-symbol, x20 six-symbol batch
  5. get_rt_ticker(num=1000)        — x20 on US.AAPL and HK.00700
  6. request_history_kline          — K_1M page1+page2 (max_count=1000) and K_DAY ~1y,
                                      on US.AAPL / US.TSLA / HK.00700

Quota: <=23 subscription slots (unsubscribe_all after the mandatory 60s wait),
<=3 historical K-line slots. Checks remaining history quota before running.

Note: get_stock_quote / get_rt_ticker read OpenD's local subscription cache;
request_history_kline goes upstream to moomoo servers. Live tick streaming cadence
is only meaningful in-session (2026-07-03: US closed for observed July 4, HK lunch
break 12:00-13:00 HKT) — request latencies below are session-independent.
"""

import json
import statistics
import threading
import time
from datetime import datetime
from pathlib import Path

from moomoo import (
    AuType,
    KLType,
    OpenQuoteContext,
    OrderBookHandlerBase,
    RET_OK,
    StockQuoteHandlerBase,
    SubType,
    TickerHandlerBase,
)

HOST, PORT = '127.0.0.1', 11111
FULL_SUB_SYMBOLS = ['US.AAPL', 'US.TSLA', 'US.NVDA', 'US.MSFT', 'HK.00700', 'HK.09988']
BATCH_SYMBOLS = ['US.AMD', 'US.AMZN', 'US.META', 'US.GOOG', 'US.NFLX']
HISTORY_SYMBOLS = ['US.AAPL', 'US.TSLA', 'HK.00700']
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
    print(f'{label:<38} n={st["n"]:<3} min={st["min"]:6.1f}  med={st["median"]:6.1f}  '
          f'mean={st["mean"]:6.1f}  p95={st["p95"]:6.1f}  max={st["max"]:6.1f}')


first_push = {}          # (code, subtype) -> monotonic ms of first push
push_lock = threading.Lock()

def record_push(subtype, codes):
    t = ms()
    with push_lock:
        for code in codes:
            first_push.setdefault((code, subtype), t)


class QuoteHandler(StockQuoteHandlerBase):
    def on_recv_rsp(self, rsp_pb):
        ret, data = super().on_recv_rsp(rsp_pb)
        if ret == RET_OK:
            record_push('QUOTE', set(data['code']))
        return ret, data


class TickerHandler(TickerHandlerBase):
    def on_recv_rsp(self, rsp_pb):
        ret, data = super().on_recv_rsp(rsp_pb)
        if ret == RET_OK and len(data):
            record_push('TICKER', set(data['code']))
        return ret, data


class OrderBookHandler(OrderBookHandlerBase):
    def on_recv_rsp(self, rsp_pb):
        ret, data = super().on_recv_rsp(rsp_pb)
        if ret == RET_OK:
            record_push('ORDER_BOOK', {data['code']})
        return ret, data


def main():
    results = {'started': datetime.now().astimezone().isoformat()}

    # ---- 1. connection setup ----
    connect_ms, ctx = timed(OpenQuoteContext, host=HOST, port=PORT)
    print(f'{ms():7.0f}ms  OpenQuoteContext #1 (cold) : {connect_ms:.1f} ms')
    connect2_ms, ctx2 = timed(OpenQuoteContext, host=HOST, port=PORT)
    ctx2.close()
    print(f'{ms():7.0f}ms  OpenQuoteContext #2        : {connect2_ms:.1f} ms')
    results['connect_ms'] = [connect_ms, connect2_ms]

    ctx.set_handler(QuoteHandler())
    ctx.set_handler(TickerHandler())
    ctx.set_handler(OrderBookHandler())

    # ---- history quota pre-flight ----
    q_ms, (ret, quota) = timed(ctx.get_history_kl_quota, get_detail=False)
    print(f'{ms():7.0f}ms  history quota ({q_ms:.0f} ms): {quota if ret == RET_OK else f"ERR {quota}"}')
    results['history_quota_before'] = str(quota)

    # ---- 2. per-symbol cold subscribe (QUOTE + ORDER_BOOK + TICKER) ----
    print('\n--- subscribe: per-symbol, QUOTE+ORDER_BOOK+TICKER in one call ---')
    sub_samples, sub_return_at = [], {}
    for sym in FULL_SUB_SYMBOLS:
        el, (ret, err) = timed(ctx.subscribe, [sym],
                               [SubType.QUOTE, SubType.ORDER_BOOK, SubType.TICKER],
                               is_first_push=True, extended_time=True)
        sub_return_at[sym] = ms()
        status = 'ok' if ret == RET_OK else f'ERR {err}'
        print(f'{ms():7.0f}ms  subscribe {sym:<10}: {el:7.1f} ms  [{status}]')
        if ret == RET_OK:
            sub_samples.append(el)
        time.sleep(0.1)
    results['subscribe_per_symbol_ms'] = sub_samples

    # ---- 3. batch watchlist subscribe (TICKER x5, one call) ----
    el, (ret, err) = timed(ctx.subscribe, BATCH_SYMBOLS, [SubType.TICKER],
                           is_first_push=True, extended_time=True)
    batch_return_at = ms()
    print(f'\n{ms():7.0f}ms  batch subscribe 5 symbols x TICKER: {el:7.1f} ms  '
          f'[{"ok" if ret == RET_OK else f"ERR {err}"}]')
    results['subscribe_batch5_ticker_ms'] = el

    # ---- wait for first pushes ----
    expected = {(s, t) for s in FULL_SUB_SYMBOLS for t in ('QUOTE', 'ORDER_BOOK', 'TICKER')}
    expected |= {(s, 'TICKER') for s in BATCH_SYMBOLS}
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        with push_lock:
            if expected <= set(first_push):
                break
        time.sleep(0.05)

    print('\n--- subscribe-return -> first push (cached first-push path) ---')
    push_deltas = {}
    for (code, subtype), t in sorted(first_push.items()):
        base = sub_return_at.get(code, batch_return_at)
        push_deltas[f'{code}/{subtype}'] = round(t - base, 1)
    missing = sorted(expected - set(first_push))
    by_type = {}
    for key, d in push_deltas.items():
        by_type.setdefault(key.split('/')[1], []).append(d)
    for subtype, deltas in sorted(by_type.items()):
        fmt(f'first push {subtype}', stats(deltas))
    if missing:
        print(f'no first push seen for: {", ".join(f"{c}/{t}" for c, t in missing)} '
              f'(expected while market closed for TICKER)')
    results['first_push_delta_ms'] = push_deltas
    results['first_push_missing'] = [f'{c}/{t}' for c, t in missing]

    # ---- 4. get_stock_quote (OpenD local cache) ----
    print('\n--- get_stock_quote (subscription cache read) ---')
    single, batch = [], []
    for _ in range(QUERY_ITERS):
        el, (ret, _) = timed(ctx.get_stock_quote, ['US.AAPL'])
        if ret == RET_OK:
            single.append(el)
        time.sleep(0.05)
    for _ in range(QUERY_ITERS):
        el, (ret, _) = timed(ctx.get_stock_quote, FULL_SUB_SYMBOLS)
        if ret == RET_OK:
            batch.append(el)
        time.sleep(0.05)
    fmt('get_stock_quote 1 symbol', stats(single))
    fmt('get_stock_quote 6 symbols', stats(batch))
    results['get_stock_quote_1sym_ms'] = single
    results['get_stock_quote_6sym_ms'] = batch

    # ---- 5. get_rt_ticker (OpenD local cache) ----
    print('\n--- get_rt_ticker num=1000 (subscription cache read) ---')
    for sym in ['US.AAPL', 'HK.00700']:
        samples, rows = [], 0
        for _ in range(QUERY_ITERS):
            el, (ret, df) = timed(ctx.get_rt_ticker, sym, 1000)
            if ret == RET_OK:
                samples.append(el)
                rows = len(df)
            time.sleep(0.05)
        fmt(f'get_rt_ticker {sym} ({rows} rows)', stats(samples))
        results[f'get_rt_ticker_{sym}_ms'] = samples
        results[f'get_rt_ticker_{sym}_rows'] = rows

    # ---- 6. request_history_kline (upstream server round-trip) ----
    print('\n--- request_history_kline (goes upstream to moomoo servers) ---')
    hist = {}
    for sym in HISTORY_SYMBOLS:
        el, (ret, df, page_key) = timed(
            ctx.request_history_kline, sym, start='2026-06-29', end='2026-07-03',
            ktype=KLType.K_1M, autype=AuType.QFQ, max_count=1000)
        n = len(df) if ret == RET_OK else 0
        more = 'more pages' if page_key else 'complete'
        print(f'{sym:<10} K_1M  page1: {el:7.1f} ms  ({n} bars, {more})')
        hist[f'{sym}/K_1M/page1'] = {'ms': el, 'bars': n, 'has_more': bool(page_key)}
        if ret == RET_OK and page_key:
            el2, (ret2, df2, _) = timed(
                ctx.request_history_kline, sym, start='2026-06-29', end='2026-07-03',
                ktype=KLType.K_1M, autype=AuType.QFQ, max_count=1000, page_req_key=page_key)
            n2 = len(df2) if ret2 == RET_OK else 0
            print(f'{sym:<10} K_1M  page2: {el2:7.1f} ms  ({n2} bars)')
            hist[f'{sym}/K_1M/page2'] = {'ms': el2, 'bars': n2}
        time.sleep(0.5)
        el, (ret, df, page_key) = timed(
            ctx.request_history_kline, sym, start='2025-07-03', end='2026-07-03',
            ktype=KLType.K_DAY, autype=AuType.QFQ, max_count=1000)
        n = len(df) if ret == RET_OK else 0
        print(f'{sym:<10} K_DAY 1yr  : {el:7.1f} ms  ({n} bars)')
        hist[f'{sym}/K_DAY'] = {'ms': el, 'bars': n}
        time.sleep(0.5)
    results['history_kline'] = hist

    _, (ret, quota) = timed(ctx.get_history_kl_quota, get_detail=False)
    print(f'\nhistory quota after: {quota if ret == RET_OK else f"ERR {quota}"}')
    results['history_quota_after'] = str(quota)

    # ---- teardown: wait out the 1-minute minimum, then release quota ----
    wait = 61 - (ms() - sub_return_at[FULL_SUB_SYMBOLS[0]]) / 1000
    if wait > 0:
        print(f'waiting {wait:.0f}s to satisfy the 1-minute minimum before unsubscribing...')
        time.sleep(wait)
    el, (ret, err) = timed(ctx.unsubscribe_all)
    print(f'unsubscribe_all: {el:.1f} ms  [{"ok" if ret == RET_OK else f"ERR {err}"}]')
    results['unsubscribe_all_ms'] = el
    ctx.close()

    CAPTURE_DIR.mkdir(exist_ok=True)
    out = CAPTURE_DIR / f'moomoo_latency_{datetime.now():%Y%m%d_%H%M%S}.json'
    out.write_text(json.dumps(results, indent=1))
    print(f'raw samples -> {out}')


if __name__ == '__main__':
    main()
