#!/usr/bin/env python3
"""Prototype: build 10s OHLCV bars from live moomoo tick pushes.

Reference implementation for eTape's Go aggregator:
- bucket by EXCHANGE timestamp (tick 'time'), floored to 10s boundaries
- first push seeds history from OpenD's tick cache, then live pushes take over
- emit a bar when a tick arrives for a later bucket (watermark-style)
- track buy/sell volume delta from tick direction
"""
import sys
import time
from datetime import datetime

from moomoo import OpenQuoteContext, TickerHandlerBase, SubType, SecurityFirm, RET_OK

SYMBOL = sys.argv[1] if len(sys.argv) > 1 else "HK.00700"
DURATION = int(sys.argv[2]) if len(sys.argv) > 2 else 75
BUCKET = 10  # seconds


def parse_ts(s):
    fmt = "%Y-%m-%d %H:%M:%S.%f" if "." in s else "%Y-%m-%d %H:%M:%S"
    return datetime.strptime(s, fmt)


class Bar:
    __slots__ = ("open", "high", "low", "close", "vol", "buy_vol", "sell_vol", "ticks")

    def __init__(self):
        self.open = None
        self.high = float("-inf")
        self.low = float("inf")
        self.close = None
        self.vol = 0
        self.buy_vol = 0
        self.sell_vol = 0
        self.ticks = 0

    def add(self, price, vol, direction):
        if self.open is None:
            self.open = price
        self.high = max(self.high, price)
        self.low = min(self.low, price)
        self.close = price
        self.vol += vol
        self.ticks += 1
        if direction == "BUY":
            self.buy_vol += vol
        elif direction == "SELL":
            self.sell_vol += vol


bars = {}     # "HH:MM:SS" bucket start -> Bar
emitted = set()


def emit(key, partial=False):
    b = bars[key]
    delta = b.buy_vol - b.sell_vol
    tag = "PARTIAL" if partial else "BAR    "
    print(
        f"{tag} {key}  O={b.open:<9g} H={b.high:<9g} L={b.low:<9g} C={b.close:<9g} "
        f"vol={b.vol:<10g} ticks={b.ticks:<5} delta(buy-sell)={delta:+g}",
        flush=True,
    )
    emitted.add(key)


class TickerAgg(TickerHandlerBase):
    def on_recv_rsp(self, rsp_pb):
        ret, df = super().on_recv_rsp(rsp_pb)
        if ret != RET_OK:
            return ret, df
        for _, row in df.iterrows():
            ts = parse_ts(row["time"])
            bucket = ts.replace(second=(ts.second // BUCKET) * BUCKET, microsecond=0)
            key = bucket.strftime("%H:%M:%S")
            if key not in bars:
                # watermark: a tick for a new bucket closes all earlier open bars
                for k in sorted(bars):
                    if k < key and k not in emitted:
                        emit(k)
                bars[key] = Bar()
            bars[key].add(row["price"], row["volume"], str(row["ticker_direction"]))
        return RET_OK, df


kwargs = {"host": "127.0.0.1", "port": 11111}
if SYMBOL.startswith("CC."):
    kwargs["security_firm"] = SecurityFirm.FUTUSG
ctx = OpenQuoteContext(**kwargs)
ctx.set_handler(TickerAgg())
ret, err = ctx.subscribe([SYMBOL], [SubType.TICKER], subscribe_push=True)
if ret != RET_OK:
    print(f"SUBSCRIBE FAILED: {err}", flush=True)
    ctx.close()
    sys.exit(2)

print(f"Subscribed {SYMBOL}; aggregating {BUCKET}s bars for {DURATION}s...", flush=True)
time.sleep(DURATION)

for k in sorted(bars):
    if k not in emitted:
        emit(k, partial=True)
ctx.close()
