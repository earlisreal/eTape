#!/usr/bin/env python3
"""Three-venue order-latency benchmark (Monday checklist §3) — routing input.

Venues:
  tz      TradeZero LIVE   (REST + Portfolio WS)          ⚠️ real money
  alpaca  Alpaca PAPER     (REST + trade_updates WS)
  moomoo  moomoo LIVE      (OpenD TCP via Python SDK)     ⚠️ real money

Per venue, per cycle: BUY 1 share marketable-limit → ack → fill → SELL 1 share
marketable-limit → ack → fill. Records place→api-return, place→ack-push,
place→fill-push for every order, warm vs cold (first call after connect = cold).
Raw pushes/frames saved to prototypes/captures/ for the golden-file corpus.

Safety (per checklist + CLAUDE.md rule):
  - live legs (tz, moomoo) run ONLY with --live-go AND during RTH (09:30–16:00 ET);
    --eth extends the window to the full US session 04:00–20:00 ET (authorized by
    Earl 2026-07-06 for the pre-market run) and switches the venues to their
    extended-hours order forms: TZ TIF Day_Plus, Alpaca extended_hours=true,
    moomoo fill_outside_rth=True — all still 1-share marketable LIMITs
  - 1 share, long side only, marketable LIMIT (never market), price guard <= $30
  - flatten immediately; positions verified flat at the end (loud alarm if not)
  - abort a venue if spread > 2% (don't benchmark into a bad tape)

Usage:
  python3 venue_order_latency_bench.py --venues alpaca                 # paper only
  python3 venue_order_latency_bench.py --venues tz,alpaca,moomoo --live-go
"""

import argparse
import http.client
import json
import sys
import threading
import time
from datetime import datetime, timezone
from pathlib import Path
from zoneinfo import ZoneInfo

SYMBOL_DEFAULT = 'F'            # cheap, liquid, penny spread
PRICE_GUARD = 30.0              # abort if last > this (cheap-symbol rule)
SPREAD_GUARD = 0.02             # abort if (ask-bid)/mid > 2%
BUY_BUFFER = 0.03               # marketable limit: ask + buffer / bid - buffer
CAPTURE_DIR = Path(__file__).parent / 'captures'
ET = ZoneInfo('America/New_York')
ETH_MODE = False                # set by --eth: extended-hours order forms

creds = json.load(open(Path.home() / '.eJournal' / 'credentials.json'))

T0 = time.monotonic()
def ms() -> float:
    return (time.monotonic() - T0) * 1000


def in_session() -> bool:
    """RTH normally; full US session 04:00-20:00 ET with --eth."""
    now = datetime.now(ET)
    if now.weekday() >= 5:
        return False
    if ETH_MODE:
        return (4, 0) <= (now.hour, now.minute) < (20, 0)
    return (9, 30) <= (now.hour, now.minute) < (16, 0)


def nbbo(symbol: str):
    """Real-time bid/ask/last via moomoo LV3 snapshot."""
    from moomoo import OpenQuoteContext, RET_OK
    ctx = OpenQuoteContext(host='127.0.0.1', port=11111)
    try:
        ret, df = ctx.get_market_snapshot([f'US.{symbol}'])
        if ret != RET_OK or not len(df):
            sys.exit(f'ABORT: no snapshot for US.{symbol}: {df}')
        row = df.iloc[0]
        return float(row['bid_price']), float(row['ask_price']), float(row['last_price'])
    finally:
        ctx.close()


def guard_prices(symbol: str):
    bid, ask, last = nbbo(symbol)
    if not (0 < bid < ask):
        sys.exit(f'ABORT: bad NBBO for {symbol}: bid={bid} ask={ask}')
    if last > PRICE_GUARD:
        sys.exit(f'ABORT: {symbol} last={last} > price guard ${PRICE_GUARD}')
    spread = (ask - bid) / ((ask + bid) / 2)
    if spread > SPREAD_GUARD:
        sys.exit(f'ABORT: {symbol} spread {spread:.2%} > {SPREAD_GUARD:.0%}')
    return bid, ask, last


class KeepAliveREST:
    """Persistent HTTPS connection — the Go adapters use keep-alive pools, so the
    benchmark must too (first call = cold TLS, rest warm). Retries once on a
    stale keep-alive socket."""

    def __init__(self, host, headers):
        self.host, self.headers = host, headers
        self.conn = None

    def request(self, method, path, body=None):
        data = json.dumps(body).encode() if body is not None else None
        t = time.monotonic()
        raw, status = '', 0
        for attempt in (0, 1):
            try:
                if self.conn is None:
                    self.conn = http.client.HTTPSConnection(self.host, timeout=15)
                self.conn.request(method, path, body=data, headers=self.headers)
                r = self.conn.getresponse()
                raw, status = r.read().decode(), r.status
                break
            except (http.client.HTTPException, OSError):
                self.conn = None          # stale socket — reconnect once
                if attempt == 1:
                    raise
        elapsed = (time.monotonic() - t) * 1000
        try:
            return elapsed, status, json.loads(raw)
        except ValueError:
            return elapsed, status, raw


class Timeline:
    """One order's latency milestones (all monotonic ms)."""

    def __init__(self, venue, side, cycle, cold):
        self.d = {'venue': venue, 'side': side, 'cycle': cycle, 'cold': cold,
                  't_send': None, 'api_return_ms': None, 'ack_ms': None,
                  'fill_ms': None, 'order_id': None, 'fill_price': None}

    def send(self):
        self.d['t_send'] = ms()

    def mark(self, key, t=None):
        if self.d.get(key) is None:
            self.d[key] = round((t if t is not None else ms()) - self.d['t_send'], 1)

    def row(self):
        return self.d

    def __str__(self):
        d = self.d
        return (f"{d['venue']:<7} {d['side']:<4} c{d['cycle']}{'*' if d['cold'] else ' '} "
                f"api={d['api_return_ms'] or '?':>7}  ack={d['ack_ms'] or '—':>7}  "
                f"fill={d['fill_ms'] or '—':>7}  px={d['fill_price'] or '—'}")


# ======================================================================
# TradeZero LIVE
# ======================================================================
class TZVenue:
    name = 'tz'
    live = True

    def __init__(self, symbol):
        self.symbol = symbol
        c = creds['tradeZero']
        self.rest_conn = KeepAliveREST(
            'webapi.tradezero.com',
            {'Accept': 'application/json', 'Content-Type': 'application/json',
             'TZ-API-KEY-ID': c['keyId'], 'TZ-API-SECRET-KEY': c['secretKey']})
        self.frames = []
        self.stream_ready = threading.Event()
        self.stream_failed = None
        self.acct = None
        self.route = None

    def rest(self, method, path, body=None):
        return self.rest_conn.request(method, path, body)

    def connect(self):
        _, status, body = self.rest('GET', '/v1/api/accounts')
        accounts = body.get('accounts', []) if isinstance(body, dict) else []
        if status != 200 or not accounts:
            raise RuntimeError(f'TZ accounts not visible (HTTP {status})')
        self.acct = accounts[0]['account']
        print(f'  tz account: {self.acct} ({accounts[0].get("accountType")})')

        _, _, routes_body = self.rest('GET', f'/v1/api/accounts/{self.acct}/routes')
        routes = routes_body.get('routes', []) if isinstance(routes_body, dict) else []
        (CAPTURE_DIR / 'tz_routes.json').write_text(json.dumps(routes_body, indent=1))
        stock_routes = [r['routeName'] for r in routes if 'Stock' in r.get('securityTypes', [])]
        print(f'  tz stock routes: {stock_routes or "none advertised"} (captured)')
        self.route = 'SMART' if 'SMART' in stock_routes else (stock_routes[0] if stock_routes else None)
        if self.route is None:
            raise RuntimeError('TZ: no stock route')

        threading.Thread(target=self._stream, daemon=True).start()
        if not self.stream_ready.wait(30) or self.stream_failed:
            raise RuntimeError(f'TZ stream not ready: {self.stream_failed}')
        print('  tz portfolio WS subscribed')

    def _stream(self):
        import websocket
        c = creds['tradeZero']
        try:
            ws = websocket.create_connection('wss://webapi.tradezero.com/stream/portfolio',
                                             timeout=20)
            while True:
                frame = json.loads(ws.recv())
                self.frames.append((ms(), frame))
                if frame.get('@system'):
                    st = frame.get('status')
                    if st == 'PENDING_AUTH':
                        ws.send(json.dumps({'key': c['keyId'], 'secret': c['secretKey']}))
                    elif st == 'CONNECTED':
                        ws.send(json.dumps({'accountId': self.acct,
                                            'subscriptions': ['Order', 'Position']}))
                    elif st in ('FAILED_AUTH', 'TERMINATED', 'INVALID_DATA'):
                        self.stream_failed = f'{st}: {frame.get("message")}'
                        self.stream_ready.set()
                        return
                elif frame.get('requestConfirmed'):
                    self.stream_ready.set()
                ws.settimeout(300)
        except Exception as e:  # noqa: BLE001
            self.stream_failed = f'{type(e).__name__}: {e}'
            self.stream_ready.set()

    def _watch(self, client_order_id, tl, timeout=25):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for t, f in list(self.frames):
                o = f.get('order') or {}
                if client_order_id not in (str(o.get('userOrderId', '')), o.get('clientOrderId')):
                    continue
                tl.mark('ack_ms', t)
                st = str(o.get('status') or o.get('orderStatus') or '').lower()
                if st.startswith('fill') or o.get('filledQuantity') in (1, '1'):
                    tl.mark('fill_ms', t)
                    tl.d['fill_price'] = o.get('averagePrice') or o.get('avgPrice')
                    return True
            time.sleep(0.02)
        return False

    def order(self, side, limit_price, cycle, cold):
        coid = f'ET-BENCH-{side}-{cycle}-{datetime.now(timezone.utc):%H%M%S}'
        tl = Timeline(self.name, side, cycle, cold)
        body = {'symbol': self.symbol, 'side': 'Buy' if side == 'buy' else 'Sell',
                'openClose': 'Open' if side == 'buy' else 'Close',
                'orderQuantity': 1, 'orderType': 'Limit', 'limitPrice': limit_price,
                'timeInForce': 'Day_Plus' if ETH_MODE else 'Day',
                'securityType': 'Stock', 'route': self.route,
                'clientOrderId': coid}
        tl.send()
        el, status, resp = self.rest('POST', f'/v1/api/accounts/{self.acct}/order', body)
        tl.mark('api_return_ms')
        tl.d['order_id'] = coid
        if status != 200 or not isinstance(resp, dict) or resp.get('orderStatus') == 'Rejected':
            print(f'  !! tz {side} rejected: HTTP {status} {json.dumps(resp)[:200]}')
            return tl, False
        filled = self._watch(coid, tl)
        if not filled:
            print(f'  !! tz {side} NOT filled in 25s — cancelling')
            self.rest('DELETE', f'/v1/api/accounts/{self.acct}/orders/{coid}')
        return tl, filled

    def position_qty(self):
        _, _, body = self.rest('GET', f'/v1/api/accounts/{self.acct}/positions')
        rows = body if isinstance(body, list) else (body.get('positions', []) if isinstance(body, dict) else [])
        for r in rows:
            if r.get('symbol') == self.symbol:
                return float(r.get('quantity') or r.get('qty') or 0)
        return 0.0

    def teardown(self):
        (CAPTURE_DIR / f'tz_bench_ws_{datetime.now():%Y%m%d_%H%M%S}.json').write_text(
            json.dumps({'account': self.acct,
                        'frames': [{'t_ms': round(t, 1), 'frame': f} for t, f in self.frames]},
                       indent=1))


# ======================================================================
# Alpaca PAPER
# ======================================================================
class AlpacaVenue:
    name = 'alpaca'
    live = False
    BASE = 'https://paper-api.alpaca.markets'

    def __init__(self, symbol):
        self.symbol = symbol
        c = creds['alpaca']
        self.rest_conn = KeepAliveREST(
            'paper-api.alpaca.markets',
            {'APCA-API-KEY-ID': c['keyId'], 'APCA-API-SECRET-KEY': c['secretKey'],
             'Accept': 'application/json', 'Content-Type': 'application/json'})
        self.events = []            # (ms, opcode, parsed_or_raw)
        self.stream_ready = threading.Event()
        self.stream_failed = None

    def rest(self, method, path, body=None):
        return self.rest_conn.request(method, path, body)

    def connect(self):
        el, status, acct = self.rest('GET', '/v2/account')
        if status != 200:
            raise RuntimeError(f'alpaca account error: HTTP {status} {acct}')
        print(f'  alpaca paper account: {acct.get("account_number")} '
              f'status={acct.get("status")} (GET /account {el:.0f}ms cold)')
        threading.Thread(target=self._stream, daemon=True).start()
        if not self.stream_ready.wait(20) or self.stream_failed:
            raise RuntimeError(f'alpaca stream not ready: {self.stream_failed}')
        print('  alpaca trade_updates listening')

    def _stream(self):
        import websocket
        from websocket import ABNF
        c = creds['alpaca']
        try:
            ws = websocket.create_connection(self.BASE.replace('https', 'wss') + '/stream',
                                             timeout=20)
            ws.send(json.dumps({'action': 'auth', 'key': c['keyId'], 'secret': c['secretKey']}))
            while True:
                opcode, data = ws.recv_data()
                t = ms()
                binary = opcode == ABNF.OPCODE_BINARY
                try:
                    parsed = json.loads(data)
                    enc = 'json-in-binary' if binary else 'json-text'
                except (ValueError, UnicodeDecodeError):
                    try:
                        import msgpack
                        parsed = msgpack.unpackb(data)
                        enc = 'msgpack'
                    except Exception:  # noqa: BLE001
                        parsed, enc = {'raw_hex': bytes(data)[:64].hex()}, 'unknown'
                self.events.append((t, enc, parsed))
                stream, payload = parsed.get('stream'), parsed.get('data', {})
                if stream == 'authorization':
                    if payload.get('status') == 'authorized':
                        ws.send(json.dumps({'action': 'listen',
                                            'data': {'streams': ['trade_updates']}}))
                    else:
                        self.stream_failed = f'auth failed: {parsed}'
                        self.stream_ready.set()
                        return
                elif stream == 'listening':
                    self.stream_ready.set()
                ws.settimeout(300)
        except Exception as e:  # noqa: BLE001
            self.stream_failed = f'{type(e).__name__}: {e}'
            self.stream_ready.set()

    def _watch(self, client_order_id, tl, timeout=25):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for t, _, parsed in list(self.events):
                if parsed.get('stream') != 'trade_updates':
                    continue
                d = parsed.get('data', {})
                if (d.get('order') or {}).get('client_order_id') != client_order_id:
                    continue
                ev = d.get('event')
                if ev == 'new':
                    tl.mark('ack_ms', t)
                elif ev in ('fill', 'partial_fill'):
                    tl.mark('ack_ms', t)
                    tl.mark('fill_ms', t)
                    tl.d['fill_price'] = d.get('price')
                    if ev == 'fill':
                        return True
                elif ev in ('rejected', 'canceled', 'expired'):
                    return False
            time.sleep(0.02)
        return False

    def order(self, side, limit_price, cycle, cold):
        coid = f'et-bench-{side}-{cycle}-{datetime.now(timezone.utc):%H%M%S%f}'
        tl = Timeline(self.name, side, cycle, cold)
        body = {'symbol': self.symbol, 'qty': '1', 'side': side, 'type': 'limit',
                'limit_price': f'{limit_price:.2f}', 'time_in_force': 'day',
                'client_order_id': coid}
        if ETH_MODE:
            body['extended_hours'] = True
        tl.send()
        el, status, resp = self.rest('POST', '/v2/orders', body)
        tl.mark('api_return_ms')
        tl.d['order_id'] = resp.get('id') if isinstance(resp, dict) else None
        if status != 200:
            print(f'  !! alpaca {side} rejected: HTTP {status} {json.dumps(resp)[:200]}')
            return tl, False
        filled = self._watch(coid, tl)
        if not filled and tl.d['order_id']:
            print(f'  !! alpaca {side} not filled in 25s — cancelling')
            self.rest('DELETE', f'/v2/orders/{tl.d["order_id"]}')
        return tl, filled

    def position_qty(self):
        _, status, body = self.rest('GET', f'/v2/positions/{self.symbol}')
        if status == 404:
            return 0.0
        return float(body.get('qty', 0)) if isinstance(body, dict) else 0.0

    def teardown(self):
        (CAPTURE_DIR / f'alpaca_bench_ws_{datetime.now():%Y%m%d_%H%M%S}.json').write_text(
            json.dumps([{'t_ms': round(t, 1), 'encoding': e, 'frame': p}
                        for t, e, p in self.events], indent=1, default=str))
        encs = {e for _, e, _ in self.events}
        print(f'  alpaca frame encodings observed: {encs}')


# ======================================================================
# moomoo LIVE (OpenD)
# ======================================================================
class MoomooVenue:
    name = 'moomoo'
    live = True

    def __init__(self, symbol):
        self.symbol = f'US.{symbol}'
        self.ctx = None
        self.acc_id = None
        self.pushes = []            # (ms, kind, record)

    def connect(self):
        from moomoo import (OpenSecTradeContext, RET_OK, SecurityFirm, TradeDealHandlerBase,
                            TradeOrderHandlerBase, TrdEnv, TrdMarket)
        bench = self

        class OrderPush(TradeOrderHandlerBase):
            def on_recv_rsp(self, rsp_pb):
                ret, data = super().on_recv_rsp(rsp_pb)
                if ret == RET_OK and len(data):
                    for rec in data.to_dict('records'):
                        bench.pushes.append((ms(), 'order', rec))
                return ret, data

        class DealPush(TradeDealHandlerBase):
            def on_recv_rsp(self, rsp_pb):
                ret, data = super().on_recv_rsp(rsp_pb)
                if ret == RET_OK and len(data):
                    for rec in data.to_dict('records'):
                        bench.pushes.append((ms(), 'deal', rec))
                return ret, data

        self.ctx = OpenSecTradeContext(filter_trdmarket=TrdMarket.NONE,
                                       host='127.0.0.1', port=11111,
                                       security_firm=SecurityFirm.FUTUSG)
        self.ctx.set_handler(OrderPush())
        self.ctx.set_handler(DealPush())
        ret, df = self.ctx.get_acc_list()
        if ret != RET_OK:
            raise RuntimeError(f'moomoo acc list: {df}')
        real = [r for r in df.to_dict('records')
                if r.get('trd_env') == 'REAL' and 'US' in (r.get('trdmarket_auth') or [])
                and r.get('acc_role') != 'MASTER']
        if not real:
            raise RuntimeError('moomoo: no non-MASTER REAL account with US auth')
        self.acc_id = real[0]['acc_id']
        print(f'  moomoo live acc: {self.acc_id} (card …{str(real[0].get("uni_card_num"))[-4:]})')

    def _watch(self, order_id, tl, timeout=25):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for t, kind, rec in list(self.pushes):
                if str(rec.get('order_id')) != str(order_id):
                    continue
                if kind == 'order':
                    tl.mark('ack_ms', t)
                    if rec.get('order_status') == 'FILLED_ALL':
                        tl.mark('fill_ms', t)
                        tl.d['fill_price'] = rec.get('dealt_avg_price')
                        return True
                elif kind == 'deal':
                    tl.mark('fill_ms', t)
                    tl.d['fill_price'] = rec.get('price')
                    return True
            time.sleep(0.02)
        return False

    def order(self, side, limit_price, cycle, cold):
        from moomoo import OrderType, RET_OK, TrdEnv, TrdSide
        tl = Timeline(self.name, side, cycle, cold)
        tl.send()
        ret, data = self.ctx.place_order(
            price=round(limit_price, 2), qty=1, code=self.symbol,
            trd_side=TrdSide.BUY if side == 'buy' else TrdSide.SELL,
            order_type=OrderType.NORMAL, trd_env=TrdEnv.REAL, acc_id=self.acc_id,
            remark=f'ET-BENCH-{side}-{cycle}', fill_outside_rth=ETH_MODE)
        tl.mark('api_return_ms')
        if ret != RET_OK:
            print(f'  !! moomoo {side} place_order error: {data}')
            return tl, False
        order_id = data.iloc[0]['order_id']
        tl.d['order_id'] = str(order_id)
        filled = self._watch(order_id, tl)
        if not filled:
            print(f'  !! moomoo {side} NOT filled in 25s — cancelling')
            from moomoo import ModifyOrderOp
            self.ctx.modify_order(ModifyOrderOp.CANCEL, order_id, 0, 0,
                                  trd_env=TrdEnv.REAL, acc_id=self.acc_id)
        return tl, filled

    def position_qty(self):
        from moomoo import RET_OK, TrdEnv
        ret, df = self.ctx.position_list_query(code=self.symbol, trd_env=TrdEnv.REAL,
                                               acc_id=self.acc_id, refresh_cache=True)
        if ret != RET_OK or not len(df):
            return 0.0
        return float(df.iloc[0]['qty'])

    def teardown(self):
        (CAPTURE_DIR / f'moomoo_bench_pushes_{datetime.now():%Y%m%d_%H%M%S}.json').write_text(
            json.dumps([{'t_ms': round(t, 1), 'kind': k, 'rec': r}
                        for t, k, r in self.pushes], indent=1, default=str))
        if self.ctx:
            self.ctx.close()


# ======================================================================
VENUES = {'tz': TZVenue, 'alpaca': AlpacaVenue, 'moomoo': MoomooVenue}


def run_venue(venue, cycles, timelines):
    print(f'\n=== {venue.name} ===')
    venue.connect()
    start_qty = venue.position_qty()
    if start_qty != 0:
        print(f'  !! pre-existing {venue.symbol if hasattr(venue, "symbol") else ""} '
              f'position qty={start_qty} — SKIPPING venue (won\'t mix with bench)')
        return
    for cycle in range(1, cycles + 1):
        bid, ask, _ = guard_prices(SYMBOL)
        tl, ok = venue.order('buy', ask + BUY_BUFFER, cycle, cold=(cycle == 1))
        timelines.append(tl.row())
        print(f'  {tl}')
        if not ok:
            print(f'  buy not filled — stopping {venue.name} cycles')
            break
        time.sleep(0.5)
        bid, ask, _ = guard_prices(SYMBOL)
        sell_tl, sell_ok = venue.order('sell', max(bid - BUY_BUFFER, 0.01), cycle, cold=False)
        timelines.append(sell_tl.row())
        print(f'  {sell_tl}')
        retries = 0
        while not sell_ok and retries < 3:
            retries += 1
            print(f'  !! flatten retry {retries} (more aggressive)')
            bid, ask, _ = nbbo(SYMBOL)
            sell_tl, sell_ok = venue.order('sell', max(round(bid * 0.995, 2), 0.01),
                                           cycle, cold=False)
            timelines.append(sell_tl.row())
            print(f'  {sell_tl}')
        time.sleep(1.0)
    qty = venue.position_qty()
    if qty != 0:
        print(f'\n  !!!! {venue.name} POSITION NOT FLAT: qty={qty} — FLATTEN MANUALLY NOW')
    else:
        print(f'  {venue.name}: flat confirmed')


def main():
    global SYMBOL, ETH_MODE
    ap = argparse.ArgumentParser()
    ap.add_argument('--venues', default='alpaca')
    ap.add_argument('--symbol', default=SYMBOL_DEFAULT)
    ap.add_argument('--cycles-live', type=int, default=2)
    ap.add_argument('--cycles-paper', type=int, default=5)
    ap.add_argument('--live-go', action='store_true',
                    help='required for tz/moomoo legs (real money)')
    ap.add_argument('--eth', action='store_true',
                    help='extended-hours mode: 04:00-20:00 ET window; TZ Day_Plus, '
                         'Alpaca extended_hours, moomoo fill_outside_rth')
    args = ap.parse_args()
    SYMBOL = args.symbol.upper()
    ETH_MODE = args.eth

    wanted = [v.strip() for v in args.venues.split(',') if v.strip()]
    for v in wanted:
        if v not in VENUES:
            sys.exit(f'unknown venue {v}')
    live_wanted = [v for v in wanted if VENUES[v].live]
    if live_wanted:
        if not args.live_go:
            sys.exit(f'ABORT: {live_wanted} are LIVE venues — pass --live-go '
                     '(requires Earl\'s explicit in-conversation authorization)')
        if not in_session():
            sys.exit(f'ABORT: outside the allowed window '
                     f'({"04:00-20:00" if ETH_MODE else "RTH"}); '
                     f'ET now = {datetime.now(ET):%H:%M}')

    CAPTURE_DIR.mkdir(exist_ok=True)
    bid, ask, last = guard_prices(SYMBOL)
    print(f'symbol {SYMBOL}: bid={bid} ask={ask} last={last} '
          f'(guards: <=${PRICE_GUARD}, spread<={SPREAD_GUARD:.0%}) ET={datetime.now(ET):%H:%M:%S}')

    timelines = []
    venues = []
    try:
        for name in wanted:
            venue = VENUES[name](SYMBOL)
            venues.append(venue)
            cycles = args.cycles_live if venue.live else args.cycles_paper
            try:
                run_venue(venue, cycles, timelines)
            except Exception as e:  # noqa: BLE001
                print(f'  !! {name} failed: {type(e).__name__}: {e}')
    finally:
        for v in venues:
            try:
                v.teardown()
            except Exception as e:  # noqa: BLE001
                print(f'  teardown {v.name}: {e}')
        out = CAPTURE_DIR / f'venue_latency_{datetime.now():%Y%m%d_%H%M%S}.json'
        out.write_text(json.dumps({'symbol': SYMBOL, 'timelines': timelines},
                                  indent=1, default=str))
        print(f'\ntimelines -> {out}')

    print('\n=== SUMMARY (ms; * = cold) ===')
    print(f'{"venue":<8}{"side":<6}{"cyc":<5}{"api-return":>11}{"ack-push":>10}{"fill-push":>10}')
    for r in timelines:
        print(f'{r["venue"]:<8}{r["side"]:<6}{r["cycle"]}{"*" if r["cold"] else " ":<4}'
              f'{r["api_return_ms"] or "—":>11}{r["ack_ms"] or "—":>10}{r["fill_ms"] or "—":>10}')


if __name__ == '__main__':
    main()
