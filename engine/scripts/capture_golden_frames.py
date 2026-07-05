#!/usr/bin/env python3
"""Capture real OpenD wire frames for the Go codec golden corpus.

Requires OpenD running on 127.0.0.1:11111. Read-only market-data calls ONLY —
never trades. Encryption must be OFF (the SDK default on localhost), so bodies
are plaintext protobuf and the header SHA1 is over the plaintext body — exactly
the Go codec's target.

It hooks the SDK's two wire choke points (verified against the installed SDK):
  * c2s: NetManager.send(conn_id, data) — data is one complete framed request.
  * s2c: open_context_base.parse_rsp(...) — on ParseRspErr.OK, data[:total_len]
         is one complete inbound frame. (parse_rsp is a bound global at the call
         site via `from .utils import *`, so we patch it in open_context_base,
         not in utils.)

InitConnect (protoID 1001) is excluded from the committed corpus in BOTH
directions: its S2C carries loginUserID (PII) and the repo is public. The codec
is protoID-agnostic, so KeepAlive + the market-data frames validate it fully.

Run with the default PATH (the pyenv python3 that has `moomoo` installed); do NOT
prepend Homebrew to PATH, which would shadow pyenv and break `import moomoo`.
"""
import binascii
import datetime as dt
import hashlib
import json
import os
import time

import moomoo
from moomoo.common import open_context_base, utils
from moomoo.common.constant import ProtoId
from moomoo.common.network_manager import NetManager
from moomoo.common.sys_config import SysConfig
from moomoo.common.utils import ParseRspErr

assert not SysConfig.is_proto_encrypt(), "encryption must be OFF for golden capture"

HOST = "127.0.0.1"
PORT = 11111

OUT = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "internal", "feed", "opend", "testdata", "golden",
)
os.makedirs(OUT, exist_ok=True)

# Plan 2 Qot protocol surface -> per-protocol golden fixture file. Request
# protos (Sub/GetBasicQot/GetKL/GetTicker/GetOrderBook/RequestHistoryKL) carry
# both c2s and s2c frames in one file; push protos (Update*) are s2c-only and
# only populate if the market was live during capture. None of these carry
# account data (loginUserID/connAESKey live only in 1001/1002, excluded
# above), so the whole Qot surface is public-repo safe.
QOT_FILES = {
    ProtoId.Qot_Sub: "qot_sub.jsonl",
    ProtoId.Qot_GetBasicQot: "qot_getbasicqot.jsonl",
    ProtoId.Qot_GetKL: "qot_getkl.jsonl",
    ProtoId.Qot_GetTicker: "qot_getticker.jsonl",
    ProtoId.Qot_GetOrderBook: "qot_getorderbook.jsonl",
    ProtoId.Qot_RequestHistoryKL: "qot_requesthistorykl.jsonl",
    ProtoId.Qot_UpdateBasicQot: "qot_update_basicqot.jsonl",
    ProtoId.Qot_UpdateTicker: "qot_update_ticker.jsonl",
    ProtoId.Qot_UpdateOrderBook: "qot_update_orderbook.jsonl",
    ProtoId.Qot_UpdateKL: "qot_update_kl.jsonl",
}

EXCLUDE = {
    1001,  # InitConnect: S2C carries loginUserID; drop both directions (public repo)
    1002,  # GetGlobalState: S2C embeds moomoo's upstream server IPs (qotSvrIpAddr/trdSvrIpAddr) — public repo
}
captured = {}     # (proto_id, direction) -> record, deduped


def _record(proto_id, direction, frame, body):
    pid = int(proto_id)
    key = (pid, direction)
    if pid in EXCLUDE or key in captured:
        return
    if len(frame) < 44 or frame[:2] != b"FT":
        return
    captured[key] = {
        "proto_id": pid,
        "direction": direction,               # c2s = client->OpenD, s2c = OpenD->client
        "is_push": bool(ProtoId.is_proto_id_push(pid)),
        "proto_fmt_type": frame[6],
        "proto_ver": frame[7],
        "serial_no": int.from_bytes(frame[8:12], "little"),
        "body_len": len(body),
        "body_sha1_hex": hashlib.sha1(body).hexdigest(),
        "frame_hex": binascii.hexlify(frame).decode(),   # FULL 44-byte header + body (round-trip target)
        "body_hex": binascii.hexlify(body).decode(),      # body only (protobuf-decode target)
        "decoded_json": None,                             # Go test decodes selected bodies via generated types
    }


# c2s choke point: every outbound frame passes through NetManager.send(conn_id, data).
_orig_send = NetManager.send


def _send_hook(self, conn_id, data):
    try:
        b = bytes(data)
        if len(b) >= 44 and b[:2] == b"FT":
            _record(int.from_bytes(b[2:6], "little"), "c2s", b, b[44:])
    except Exception:
        pass
    return _orig_send(self, conn_id, data)


NetManager.send = _send_hook

# s2c choke point: parse_rsp returns one complete inbound frame on success.
_orig_parse = utils.parse_rsp


def _parse_hook(data, conn_id, is_encrypt):
    res = _orig_parse(data, conn_id, is_encrypt)
    try:
        if res.err == ParseRspErr.OK and res.head_dict is not None and res.total_len >= 44:
            frame = bytes(data[:res.total_len])
            _record(res.head_dict["proto_id"], "s2c", frame, frame[44:])
    except Exception:
        pass
    return res


open_context_base.parse_rsp = _parse_hook


def main():
    ctx = moomoo.OpenQuoteContext(host="127.0.0.1", port=11111)
    try:
        # Read-only calls that each exercise a distinct protoID. Market may be
        # closed (returns last-session data) — that is fine; request/response
        # frames are still real wire frames. Push frames only arrive live.
        ctx.get_global_state()                                    # 1002
        ctx.get_market_snapshot(["US.AAPL"])                     # 3203
        ctx.subscribe(
            ["US.AAPL"],
            [moomoo.SubType.QUOTE, moomoo.SubType.TICKER,
             moomoo.SubType.ORDER_BOOK, moomoo.SubType.K_1M],
        )                                                         # 3001 (+ pushes if live)
        time.sleep(3)
        ctx.get_stock_quote(["US.AAPL"])                         # 3004
        ctx.get_order_book("US.AAPL")                            # 3012
        ctx.get_cur_kline("US.AAPL", 10, moomoo.KLType.K_1M)     # 3006
        ctx.get_rt_ticker("US.AAPL", 20)                         # 3010
        time.sleep(9)                                            # >=1 KeepAlive (1004) c2s + s2c
    finally:
        ctx.close()

    manifest = {
        "sdk_version": getattr(moomoo, "__version__", "unknown"),
        "captured_frames": len(captured),
        "encryption": "off",
        "proto_fmt": "protobuf",
        "excluded_proto_ids": sorted(EXCLUDE),
        "note": ("Excluded both directions (public repo): InitConnect(1001) "
                 "S2C carries loginUserID; GetGlobalState(1002) S2C embeds "
                 "moomoo upstream server IPs. body_hex/frame_hex are byte-exact "
                 "round-trip targets; decoded_json is null (the Go test decodes "
                 "selected bodies via generated types)."),
    }
    with open(os.path.join(OUT, "manifest.json"), "w") as f:
        json.dump(manifest, f, indent=2)
        f.write("\n")
    with open(os.path.join(OUT, "frames.jsonl"), "w") as f:
        for rec in sorted(captured.values(), key=lambda r: (r["proto_id"], r["direction"])):
            f.write(json.dumps(rec) + "\n")

    print(f"wrote {len(captured)} frames to {OUT}")
    for key in sorted(captured):
        print("  captured", key)


def capture_qot(symbol: str, secs: int) -> None:
    """Subscribe QUOTE/ORDER_BOOK/TICKER/K_1M on symbol, let pushes flow for
    secs, and exercise every Plan 2 request once. The global hooks record
    all frames; Qot frames carry no account data (public-repo safe)."""
    from moomoo import AuType, KLType, RET_OK, SubType

    ctx = moomoo.OpenQuoteContext(host=HOST, port=PORT)
    try:
        ret, err = ctx.subscribe(
            [symbol],
            [SubType.QUOTE, SubType.ORDER_BOOK, SubType.TICKER, SubType.K_1M],
            subscribe_push=True, extended_time=True,
        )                                                            # 3001 (+ pushes if live)
        assert ret == RET_OK, f"subscribe failed: {err}"
        time.sleep(secs)                                             # pushes accumulate
        ctx.get_stock_quote([symbol])                                # 3004
        ctx.get_cur_kline(symbol, 100, ktype=KLType.K_1M, autype=AuType.QFQ)  # 3006
        ctx.get_rt_ticker(symbol, 100)                                # 3010
        ctx.get_order_book(symbol, num=10)                            # 3012
        today = dt.date.today().isoformat()
        ctx.request_history_kline(
            symbol, start=today, end=today,
            ktype=KLType.K_1M, autype=AuType.QFQ, max_count=100,
        )                                                             # 3103
    finally:
        ctx.close()


def write_qot_corpus(symbol: str, secs: int) -> dict:
    """Write per-protocol golden files for whatever Qot frames were captured,
    and record the capture context in manifest.json (additive: preserves the
    Plan 1 frames.jsonl manifest fields already there)."""
    written = {}
    for proto_id, filename in QOT_FILES.items():
        recs = [captured[key] for key in sorted(captured) if key[0] == proto_id]
        if not recs:
            continue
        with open(os.path.join(OUT, filename), "w") as f:
            for rec in recs:
                f.write(json.dumps(rec) + "\n")
        written[filename] = len(recs)

    manifest_path = os.path.join(OUT, "manifest.json")
    manifest = {}
    if os.path.exists(manifest_path):
        with open(manifest_path) as f:
            manifest = json.load(f)
    manifest["qot_capture"] = {
        "sdk_version": getattr(moomoo, "__version__", "unknown"),
        "captured_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "symbol": symbol,
        "secs": secs,
        "files": written,
        "excluded_proto_ids": sorted(EXCLUDE),
        "note": ("Plan 2 protocol surface: Qot_Sub/GetBasicQot/GetKL/GetTicker/"
                 "GetOrderBook/RequestHistoryKL (request+response) plus "
                 "Qot_Update* pushes when the market was live during capture. "
                 "No account data — safe for the public repo."),
    }
    with open(manifest_path, "w") as f:
        json.dump(manifest, f, indent=2)
        f.write("\n")
    return written


def main_qot(symbol: str, secs: int) -> None:
    capture_qot(symbol, secs)
    written = write_qot_corpus(symbol, secs)
    print(f"wrote qot corpus for {symbol} ({secs}s of pushes) to {OUT}")
    if not written:
        print("  (no frames captured — check OpenD connectivity/entitlements)")
    for filename, n in sorted(written.items()):
        print(f"  {filename}: {n} frame(s)")


if __name__ == "__main__":
    import argparse

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--qot", metavar="SYMBOL",
        help="capture the Plan 2 Qot protocol surface for SYMBOL "
             "(e.g. CC.BTC on weekends, US.AAPL during RTH)",
    )
    parser.add_argument(
        "--secs", type=int, default=30,
        help="seconds to let pushes accumulate after subscribing (default: 30)",
    )
    args = parser.parse_args()
    if args.qot:
        main_qot(args.qot, args.secs)
    else:
        main()
