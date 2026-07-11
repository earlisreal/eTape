#!/usr/bin/env python3
"""Capture real OpenD wire frames for the Go codec golden corpus.

Requires OpenD running on 127.0.0.1:11111. Encryption must be OFF (the SDK
default on localhost), so bodies are plaintext protobuf and the header SHA1
is over the plaintext body — exactly the Go codec's target.

Two capture surfaces:
  * Market data (main()/capture_qot/--qot): read-only calls ONLY, never trades.
  * Trade (capture_trd_paper/--trd-paper, Task 7): drives ONE real PAPER
    (SIMULATE) order through its full lifecycle -- see that function's
    docstring for the safety construction (explicit trd_env=SIMULATE on every
    write call, far-from-market price, immediate cancel, an accID leak guard
    on the SDK's auto-subscribe, and accID redaction before any trade fixture
    is written). Never pass a REAL/live account id to --trd-paper.

It hooks the SDK's two wire choke points (verified against the installed SDK):
  * c2s: NetManager.send(conn_id, data) — data is one complete framed request.
  * s2c: open_context_base.parse_rsp(...) — on ParseRspErr.OK, data[:total_len]
         is one complete inbound frame. (parse_rsp is a bound global at the call
         site via `from .utils import *`, so we patch it in open_context_base,
         not in utils.)
These hooks are global and source-agnostic: they capture frames for ANY SDK
context (quote or trade) transparently, which is what lets the Task 7 trade
capture below reuse them without adding any new instrumentation.

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
import struct
import time

import moomoo
from moomoo.common import open_context_base, utils
from moomoo.common.constant import ProtoId
from moomoo.common.network_manager import NetManager
from moomoo.common.sys_config import SysConfig
from moomoo.common.utils import MESSAGE_HEAD_FMT, ParseRspErr
from moomoo.trade.open_trade_context import OpenTradeContextBase

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
captured = {}     # (proto_id, direction) -> record, deduped (first-wins; one example per protocol)

# Task 7: protocols where the market-data corpus's "first frame wins" dedup
# would throw away exactly the thing we need -- a real order's push-by-push
# status narrative. Every frame for these proto ids is ALSO appended here, in
# arrival order, independent of (and without disturbing) the `captured` dict
# above. Empty unless/until capture_trd_paper() runs.
_ALWAYS_LOG_PUSH = {ProtoId.Trd_UpdateOrder}
trd_push_log = []


def _build_record(pid, direction, frame, body):
    return {
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


def _record(proto_id, direction, frame, body):
    pid = int(proto_id)
    if pid in EXCLUDE:
        return
    if len(frame) < 44 or frame[:2] != b"FT":
        return
    if pid in _ALWAYS_LOG_PUSH:
        trd_push_log.append(_build_record(pid, direction, frame, body))
    key = (pid, direction)
    if key in captured:
        return
    captured[key] = _build_record(pid, direction, frame, body)


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


# ---------------------------------------------------------------------------
# Task 7: real paper-trade golden-frame capture for internal/broker/moomoo's
# trade surface (Trd_SubAccPush/2008, Trd_PlaceOrder/2202, Trd_ModifyOrder/2205
# used for cancel, Trd_UpdateOrder/2208). Supersedes Task 4's hand-crafted
# testdata/gen/main.go fixture for trd_update_order.jsonl with a REAL captured
# frame -- the decoder must not care which source produced the bytes it's
# fed (Task 4's own stated design goal); this is the proof.
#
# SAFETY: PAPER (SIMULATE) ONLY, by construction:
#   - trd_env=TrdEnv.SIMULATE is passed explicitly on every place/modify call
#     below (never relies on an ambient env var), and OpenD itself rejects an
#     acc_id/trd_env mismatch -- so even a caller error here fails safe.
#   - The order is a 1-share limit priced ~50% below the last trade on a
#     cheap, liquid symbol: it cannot fill.
#   - It is cancelled immediately after the accept push is observed.
#
# ACCOUNT-ID LEAK GUARD: OpenTradeContextBase auto-subscribes Trd_SubAccPush
# on connect with the account list from Trd_GetAccList, which (on a
# multi-account OpenD login) can include a REAL account alongside the
# intended SIMULATE one. _trd_acc_allowlist filters _async_sub_acc_push
# globally so the wire-level accIDList this process ever sends is restricted
# to the caller-specified account -- a real account's numeric id must never
# reach the wire (default: empty allowlist = subscribe to nothing).
# Trd_GetAccList (2001) itself is deliberately never added to TRD_FILES
# below, so its response (which carries the full account list, incl. the
# REAL account's card_num/security_firm) is captured in memory only and
# never written to any fixture on disk.
# ---------------------------------------------------------------------------
from moomoo.common.pb import (
    Trd_ModifyOrder_pb2,
    Trd_PlaceOrder_pb2,
    Trd_SubAccPush_pb2,
    Trd_UpdateOrder_pb2,
)

TRD_OUT = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "internal", "broker", "moomoo", "testdata", "golden",
)
os.makedirs(TRD_OUT, exist_ok=True)

TRD_FILES = {
    ProtoId.Trd_SubAccPush: "trd_subaccpush.jsonl",
    ProtoId.Trd_PlaceOrder: "trd_placeorder.jsonl",
    ProtoId.Trd_ModifyOrder: "trd_modifyorder.jsonl",
    ProtoId.Trd_UpdateOrder: "trd_update_order.jsonl",  # supersedes Task 4's hand-crafted fixture
}

# (Request class or None for push-only protos, Response class).
_TRD_PB_CLASSES = {
    ProtoId.Trd_SubAccPush: (Trd_SubAccPush_pb2.Request, Trd_SubAccPush_pb2.Response),
    ProtoId.Trd_PlaceOrder: (Trd_PlaceOrder_pb2.Request, Trd_PlaceOrder_pb2.Response),
    ProtoId.Trd_ModifyOrder: (Trd_ModifyOrder_pb2.Request, Trd_ModifyOrder_pb2.Response),
    ProtoId.Trd_UpdateOrder: (None, Trd_UpdateOrder_pb2.Response),  # push: no C2S/Request exists
}

# Placeholder accID substituted into every written trade frame before commit
# (public repo). Same literal value as testdata/gen/main.go's genAccID, so
# the hand-crafted and real-captured fixtures share one obviously-fake
# account id rather than introducing a second one.
REDACTED_ACC_ID = 28881234

_trd_acc_allowlist = set()

_orig_async_sub_acc_push = OpenTradeContextBase._async_sub_acc_push


def _filtered_async_sub_acc_push(self, acc_id_list):
    filtered = [a for a in acc_id_list if a in _trd_acc_allowlist]
    return _orig_async_sub_acc_push(self, filtered)


OpenTradeContextBase._async_sub_acc_push = _filtered_async_sub_acc_push


class _OrderPushEcho(moomoo.TradeOrderHandlerBase):
    """Prints on every Trd_UpdateOrder push, purely so the driving script
    can see pushes arrive in real time; the actual capture happens via the
    module-level c2s/s2c hooks regardless of whether a handler is set."""

    def on_recv_rsp(self, rsp_pb):
        ret_code, data = super().on_recv_rsp(rsp_pb)
        if ret_code == moomoo.RET_OK:
            print("  [push] Trd_UpdateOrder received")
        return ret_code, data


def capture_trd_paper(account_id: int, market: str = "US", symbol: str = "US.F") -> None:
    """Drive one real OpenSecTradeContext through a tiny paper order's full
    lifecycle (place a far-from-market 1-share limit -> wait for the accept
    push -> cancel -> wait for the cancel push -> close) so the module-level
    hooks capture real Trd_SubAccPush/Trd_PlaceOrder/Trd_ModifyOrder/
    Trd_UpdateOrder wire frames.

    account_id MUST already be verified as a SIMULATE account authorized for
    `market` by the caller (see get_accounts.py) -- this function does not
    re-verify that itself, beyond the trd_env=SIMULATE it passes explicitly
    on every write call.
    """
    from moomoo import ModifyOrderOp, OrderType, RET_OK, SecurityFirm, TrdEnv, TrdMarket, TrdSide

    _trd_acc_allowlist.clear()
    _trd_acc_allowlist.add(account_id)
    trd_push_log.clear()

    trd_market = {"US": TrdMarket.US}[market]

    # Reference price so the limit rests far below market and can never fill.
    qctx = moomoo.OpenQuoteContext(host=HOST, port=PORT)
    try:
        ret, snap = qctx.get_market_snapshot([symbol])
        assert ret == RET_OK, f"get_market_snapshot({symbol}) failed: {snap}"
        last_price = float(snap.iloc[0]["last_price"])
    finally:
        qctx.close()
    assert last_price > 0, f"no usable last_price for {symbol}: {last_price}"
    limit_price = round(last_price * 0.5, 2)
    print(f"  reference last_price={last_price} -> far-from-market limit={limit_price}")

    ctx = moomoo.OpenSecTradeContext(
        host=HOST, port=PORT, filter_trdmarket=trd_market, security_firm=SecurityFirm.NONE,
    )
    try:
        ctx.set_handler(_OrderPushEcho())
        ret, data = ctx.place_order(
            price=limit_price, qty=1, code=symbol, trd_side=TrdSide.BUY,
            order_type=OrderType.NORMAL, trd_env=TrdEnv.SIMULATE, acc_id=account_id,
            remark="ET7CAPTURE",
        )
        assert ret == RET_OK, f"place_order failed: {data}"
        order_id = int(data.iloc[0]["order_id"])
        print(f"  placed 1-share {symbol} buy limit @ {limit_price} (order_id known locally, not printed)")
        time.sleep(5)  # let the Accepted Trd_UpdateOrder push(es) arrive

        ret, data = ctx.modify_order(
            modify_order_op=ModifyOrderOp.CANCEL, order_id=order_id, qty=0, price=0,
            trd_env=TrdEnv.SIMULATE, acc_id=account_id,
        )
        assert ret == RET_OK, f"cancel (modify_order) failed: {data}"
        time.sleep(5)  # let the Canceled Trd_UpdateOrder push(es) arrive
    finally:
        ctx.close()


def _redact_accid_and_reframe(rec: dict) -> dict:
    """Return a copy of rec with header.accID (or, for Trd_SubAccPush,
    every accIDList entry) replaced by REDACTED_ACC_ID -- via a full
    protobuf parse + field-set + SerializeToString + frame rebuild (fresh
    bodyLen and SHA1), never a raw byte patch. Ends with a self-check that
    re-parses the rebuilt frame/body exactly like the Go codec + protobuf
    library would, so a corrupt rewrite fails loudly here instead of
    surfacing as a mysterious decode failure in the Go test."""
    proto_id = rec["proto_id"]
    direction = rec["direction"]
    orig_frame = bytes.fromhex(rec["frame_hex"])
    header, body = orig_frame[:44], orig_frame[44:]
    (_, _, hdr_proto_id, proto_fmt_type, proto_ver,
     serial_no, body_len, _orig_sha20, reserve8) = struct.unpack(MESSAGE_HEAD_FMT, header)
    assert hdr_proto_id == proto_id and body_len == len(body), (
        f"proto {proto_id} {direction}: header/body_len mismatch before redaction"
    )

    req_cls, rsp_cls = _TRD_PB_CLASSES[proto_id]
    if direction == "c2s":
        msg = req_cls()
        msg.ParseFromString(body)
        if proto_id == ProtoId.Trd_SubAccPush:
            msg.c2s.accIDList[:] = [REDACTED_ACC_ID] * len(msg.c2s.accIDList)
        else:
            msg.c2s.header.accID = REDACTED_ACC_ID
    else:
        msg = rsp_cls()
        msg.ParseFromString(body)
        # Trd_SubAccPush's S2C is defined empty in the .proto (`message S2C
        # {}`) -- the ack carries zero account data, so there is nothing to
        # redact for this one proto's s2c direction.
        if proto_id != ProtoId.Trd_SubAccPush:
            msg.s2c.header.accID = REDACTED_ACC_ID

    new_body = msg.SerializeToString()
    new_sha20 = hashlib.sha1(new_body).digest()
    new_header = struct.pack(
        MESSAGE_HEAD_FMT, b"F", b"T", proto_id, proto_fmt_type,
        proto_ver, serial_no, len(new_body), new_sha20, reserve8,
    )
    new_frame = new_header + new_body

    # Self-check 1: header is self-consistent (mirrors opend.Decode's checks).
    (_, _, v_id, _, _, v_serial, v_len, v_sha, _) = struct.unpack(MESSAGE_HEAD_FMT, new_header)
    assert new_frame[:2] == b"FT" and v_id == proto_id and v_serial == serial_no
    assert v_len == len(new_body) and v_sha == new_sha20
    # Self-check 2: the redacted body still parses, and the field actually changed.
    verify_cls = req_cls if direction == "c2s" else rsp_cls
    verify_msg = verify_cls()
    verify_msg.ParseFromString(new_body)
    if proto_id == ProtoId.Trd_SubAccPush and direction == "c2s":
        assert list(verify_msg.c2s.accIDList) == [REDACTED_ACC_ID] * len(verify_msg.c2s.accIDList)
    elif proto_id == ProtoId.Trd_SubAccPush:
        pass  # s2c: empty message by design, nothing to verify
    elif direction == "c2s":
        assert verify_msg.c2s.header.accID == REDACTED_ACC_ID
    else:
        assert verify_msg.s2c.header.accID == REDACTED_ACC_ID

    new_rec = dict(rec)
    new_rec["body_len"] = len(new_body)
    new_rec["body_sha1_hex"] = new_sha20.hex()
    new_rec["frame_hex"] = binascii.hexlify(new_frame).decode()
    new_rec["body_hex"] = binascii.hexlify(new_body).decode()
    new_rec["decoded_json"] = None
    return new_rec


def write_trd_corpus(market: str, symbol: str) -> dict:
    """Write per-protocol golden files for whatever Task 7 trade frames were
    captured (accID redacted -- see _redact_accid_and_reframe), and record
    the capture context in testdata/golden/manifest.json. Trd_GetAccList
    (2001) is intentionally excluded from TRD_FILES -- see the module
    docstring above -- so it is never written here even though it was
    captured in memory."""
    written = {}
    for proto_id, filename in TRD_FILES.items():
        if proto_id in _ALWAYS_LOG_PUSH:
            # Every real push in arrival order (see trd_push_log's docstring
            # above) -- the whole point is NOT deduping to one frame here.
            recs = [r for r in trd_push_log if r["proto_id"] == proto_id]
        else:
            recs = [captured[key] for key in sorted(captured) if key[0] == proto_id]
        if not recs:
            continue
        redacted = [_redact_accid_and_reframe(r) for r in recs]
        with open(os.path.join(TRD_OUT, filename), "w") as f:
            for rec in redacted:
                f.write(json.dumps(rec) + "\n")
        written[filename] = len(redacted)

    manifest_path = os.path.join(TRD_OUT, "manifest.json")
    manifest = {}
    if os.path.exists(manifest_path):
        with open(manifest_path) as f:
            manifest = json.load(f)
    manifest["trd_capture"] = {
        "sdk_version": getattr(moomoo, "__version__", "unknown"),
        "captured_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "market": market,
        "symbol": symbol,
        "trd_env": "SIMULATE",
        "files": written,
        "redaction": (
            f"header.accID (and Trd_SubAccPush accIDList entries) replaced with "
            f"placeholder {REDACTED_ACC_ID} -- same value as "
            f"testdata/gen/main.go's genAccID -- via protobuf re-parse + "
            f"field-set + SerializeToString + frame rebuild (bodyLen/SHA1 "
            f"recomputed), never a raw byte patch. Verified by re-parsing the "
            f"rebuilt frame/body before writing. Trd_GetAccList (2001) responses "
            f"(which carry the full multi-account list, including a REAL "
            f"account on this OpenD login) were captured in memory only and "
            f"are never written to a fixture file."
        ),
        "note": (
            "Task 7 trade surface: Trd_SubAccPush(2008)/Trd_PlaceOrder(2202)/"
            "Trd_ModifyOrder(2205, used here for cancel)/Trd_UpdateOrder(2208) "
            "captured from one real tiny (1 share) far-from-market SIMULATE "
            "limit order's full lifecycle (place -> Accepted push(es) -> "
            "cancel -> Canceled push). Supersedes testdata/gen/main.go's "
            "hand-crafted trd_update_order.jsonl with a real OpenD capture."
        ),
    }
    with open(manifest_path, "w") as f:
        json.dump(manifest, f, indent=2)
        f.write("\n")
    return written


def main_trd(account_id: int, market: str, symbol: str) -> None:
    capture_trd_paper(account_id, market, symbol)
    written = write_trd_corpus(market, symbol)
    print(f"wrote trd corpus (market={market}, symbol={symbol}) to {TRD_OUT}")
    if not written:
        print("  (no frames captured — check OpenD connectivity/account/push arrival)")
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
    parser.add_argument(
        "--trd-paper", metavar="ACCOUNT_ID", type=int,
        help="drive one real SIMULATE order lifecycle (place a far-from-market "
             "limit, wait, cancel) through ACCOUNT_ID and write the moomoo trade "
             "golden corpus (Trd_SubAccPush/PlaceOrder/ModifyOrder/UpdateOrder, "
             "accID redacted) to engine/internal/broker/moomoo/testdata/golden/. "
             "ACCOUNT_ID MUST already be a verified SIMULATE account (see "
             "get_accounts.py) -- this script does not re-verify that for you.",
    )
    parser.add_argument(
        "--trd-market", default="US",
        help="trading market for --trd-paper (default: US)",
    )
    parser.add_argument(
        "--trd-symbol", default="US.F",
        help="cheap, liquid symbol for --trd-paper's far-from-market limit order (default: US.F)",
    )
    args = parser.parse_args()
    if args.qot:
        main_qot(args.qot, args.secs)
    elif args.trd_paper:
        main_trd(args.trd_paper, args.trd_market, args.trd_symbol)
    else:
        main()
