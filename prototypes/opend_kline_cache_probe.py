#!/usr/bin/env python3
"""Probe OpenD K_1M/K_DAY subscription-cache hydration.

Uses only subscribe, query_subscription, get_cur_kline, and push callbacks.
Does not call request_history_kline or any trading endpoint.
"""

import argparse
import os
import sys
import threading
import time
from datetime import datetime

from moomoo import (
    AuType,
    CurKlineHandlerBase,
    KLType,
    OpenQuoteContext,
    RET_OK,
    SubType,
)


SAMPLE_DELAYS = (0.0, 0.3, 1.0, 3.0, 5.0)


def enum_name(value) -> str:
    name = getattr(value, "name", None)
    return name if name else str(value).split(".")[-1]


def active_types(ctx: OpenQuoteContext, symbol: str) -> set[str]:
    ret, data = ctx.query_subscription(is_all_conn=True)
    if ret != RET_OK:
        raise RuntimeError(f"query_subscription failed: {data}")
    found: set[str] = set()
    for subtype, symbols in data.get("sub_list", {}).items():
        if symbol in symbols:
            found.add(enum_name(subtype))
    return found


class PushRecorder(CurKlineHandlerBase):
    def __init__(self, started: float):
        super().__init__()
        self.started = started
        self.lock = threading.Lock()
        self.pushes: list[dict] = []

    def on_recv_rsp(self, rsp_pb):
        ret, data = super().on_recv_rsp(rsp_pb)
        if ret != RET_OK:
            print(f"push decode error: {data}", file=sys.stderr)
            return ret, data

        raw_type = getattr(getattr(rsp_pb, "s2c", None), "klType", "unknown")
        kinds = data["k_type"].unique().tolist() if "k_type" in data.columns else [raw_type]
        event = {
            "elapsed_ms": round((time.monotonic() - self.started) * 1000, 1),
            "types": [enum_name(v) for v in kinds],
            "rows": len(data),
            "oldest": str(data["time_key"].iloc[0]) if len(data) else None,
            "newest": str(data["time_key"].iloc[-1]) if len(data) else None,
        }
        with self.lock:
            self.pushes.append(event)
        print(
            f"push +{event['elapsed_ms']:7.1f}ms type={','.join(event['types'])} "
            f"rows={event['rows']} range={event['oldest']} .. {event['newest']}"
        )
        return ret, data


def read_cache(ctx, symbol: str, label: str, ktype, autype) -> tuple[bool, int]:
    started = time.monotonic()
    ret, data = ctx.get_cur_kline(symbol, 1000, ktype=ktype, autype=autype)
    latency_ms = (time.monotonic() - started) * 1000
    if ret != RET_OK:
        print(f"  {label:<5} ERR latency={latency_ms:7.1f}ms message={data}")
        return False, 0
    rows = len(data)
    oldest = str(data["time_key"].iloc[0]) if rows else "-"
    newest = str(data["time_key"].iloc[-1]) if rows else "-"
    print(
        f"  {label:<5} ok  latency={latency_ms:7.1f}ms rows={rows:4d} "
        f"range={oldest} .. {newest}"
    )
    return True, rows


def parse_args():
    parser = argparse.ArgumentParser(
        description="Measure cold/warm OpenD K_1M and K_DAY cache hydration."
    )
    parser.add_argument("--symbol", default="US.AAPL", help="moomoo code; default US.AAPL")
    parser.add_argument("--host", default=os.getenv("FUTU_OPEND_HOST", "127.0.0.1"))
    parser.add_argument("--port", type=int, default=int(os.getenv("FUTU_OPEND_PORT", "11111")))
    parser.add_argument(
        "--require-cold",
        action="store_true",
        help="abort if K_1M or K_DAY is already subscribed on any OpenD connection",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if "." not in args.symbol:
        print("symbol must use moomoo format, e.g. US.AAPL", file=sys.stderr)
        return 2

    ctx = None
    started = time.monotonic()
    try:
        connect_started = time.monotonic()
        ctx = OpenQuoteContext(host=args.host, port=args.port)
        connected_at = time.monotonic()
        print(
            f"connect ok latency={(connected_at - connect_started) * 1000:.1f}ms "
            f"startup_elapsed={(connected_at - started) * 1000:.1f}ms"
        )
        before = active_types(ctx, args.symbol)
        watched = {"K_1M", "K_DAY"}
        already = sorted(before & watched)
        state = "warm" if already else "cold"
        print(f"symbol={args.symbol} pre_subscription_state={state} active={already or 'none'}")
        if args.require_cold and already:
            print("require-cold failed: choose symbol absent from current OpenD subscriptions", file=sys.stderr)
            return 2

        recorder = PushRecorder(started)
        ctx.set_handler(recorder)

        sub_started = time.monotonic()
        ret, data = ctx.subscribe(
            [args.symbol],
            [SubType.K_1M, SubType.K_DAY],
            subscribe_push=True,
            is_first_push=True,
            extended_time=True,
        )
        sub_ms = (time.monotonic() - sub_started) * 1000
        if ret != RET_OK:
            print(f"subscribe failed after {sub_ms:.1f}ms: {data}", file=sys.stderr)
            return 1
        subscribed_at = time.monotonic()
        print(
            f"subscribe ok latency={sub_ms:.1f}ms "
            f"startup_elapsed={(subscribed_at - started) * 1000:.1f}ms "
            f"at={datetime.now().astimezone().isoformat()}"
        )

        specs = (
            ("K_1M", KLType.K_1M, AuType.NONE),
            ("K_DAY", KLType.K_DAY, AuType.QFQ),
        )
        successful = {label: False for label, _, _ in specs}
        prior_rows: dict[str, int] = {}
        first_data_ms: dict[str, dict[str, float]] = {}
        sample_origin = time.monotonic()
        for delay in SAMPLE_DELAYS:
            remaining = sample_origin + delay - time.monotonic()
            if remaining > 0:
                time.sleep(remaining)
            print(f"sample +{delay:0.1f}s")
            for label, ktype, autype in specs:
                ok, rows = read_cache(ctx, args.symbol, label, ktype, autype)
                successful[label] |= ok
                if ok and rows > 0 and label not in first_data_ms:
                    received_at = time.monotonic()
                    first_data_ms[label] = {
                        "from_start": round((received_at - started) * 1000, 1),
                        "from_subscribe_start": round((received_at - sub_started) * 1000, 1),
                        "from_subscribe_ack": round((received_at - subscribed_at) * 1000, 1),
                    }
                    timing = first_data_ms[label]
                    print(
                        f"        first_nonempty {label}: "
                        f"startup={timing['from_start']:.1f}ms "
                        f"subscribe_start={timing['from_subscribe_start']:.1f}ms "
                        f"subscribe_ack={timing['from_subscribe_ack']:.1f}ms"
                    )
                change = rows - prior_rows[label] if label in prior_rows else None
                if change is not None:
                    print(f"        row_change={change:+d}")
                prior_rows[label] = rows

        time.sleep(0.25)
        with recorder.lock:
            pushes = list(recorder.pushes)
        print(
            f"summary cache_rows={prior_rows} pushes={len(pushes)} "
            f"first_nonempty_ms={first_data_ms}"
        )
        if not all(successful.values()):
            print(f"persistent cache-read failure: {successful}", file=sys.stderr)
            return 1
        return 0
    except KeyboardInterrupt:
        print("interrupted", file=sys.stderr)
        return 130
    except Exception as exc:
        print(f"probe failed: {exc}", file=sys.stderr)
        return 1
    finally:
        if ctx is not None:
            # No explicit unsubscribe: avoid disturbing subscriptions owned by
            # other eTape/OpenD clients. OpenD releases this connection later.
            ctx.close()


if __name__ == "__main__":
    raise SystemExit(main())
