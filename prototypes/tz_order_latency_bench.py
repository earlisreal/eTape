#!/usr/bin/env python3
"""TradeZero order-latency benchmark (authorized by Earl 2026-07-03).

Places ONE unfillable order — Buy 1 AAPL, Limit far below market — then cancels it.
Measures:
  1. POST /order HTTP round-trip
  2. POST → Portfolio-WS order push (ack latency)
  3. POST → order visible in GET /orders (REST visibility)
  4. DELETE /orders/{id} HTTP round-trip
  5. DELETE → WS canceled push, and → Canceled visible in GET /orders

Safety: qty=1; limit = min($50, 50% of last price); aborts unless the account is
visible and a Stock route exists; always attempts cancel; verifies no working
orders remain (loud warning + retries if cancellation cannot be confirmed).
Run during a live session (pre-market 04:00–20:00 ET). Raw WS frames are saved to
prototypes/captures/ for the golden-file test corpus.
"""

import json
import sys
import threading
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

BASE = 'https://webapi.tradezero.com'
WS_URL = 'wss://webapi.tradezero.com/stream/portfolio'
SYMBOL = 'AAPL'
QTY = 1
FALLBACK_LIMIT = 50.00          # AAPL has never been near this in a decade
ORDERS_POLL_INTERVAL = 0.45     # GET /orders limit is 2/s — stay under
CAPTURE_DIR = Path(__file__).parent / 'captures'

creds = json.load(open(Path.home() / '.eJournal' / 'credentials.json'))['tradeZero']
HEADERS = {'Accept': 'application/json', 'Content-Type': 'application/json',
           'TZ-API-KEY-ID': creds['keyId'], 'TZ-API-SECRET-KEY': creds['secretKey']}

T0 = time.monotonic()
def ms() -> float:
    return (time.monotonic() - T0) * 1000

def rest(method: str, path: str, body: dict | None = None):
    """Returns (elapsed_ms, status, parsed_or_text)."""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, headers=HEADERS, method=method)
    t = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            raw = r.read().decode()
            status = r.status
    except urllib.error.HTTPError as e:
        raw, status = e.read().decode(), e.code
    elapsed = (time.monotonic() - t) * 1000
    try:
        parsed = json.loads(raw)
    except ValueError:
        parsed = raw
    return elapsed, status, parsed


class PortfolioStream(threading.Thread):
    """Auth + subscribe, then record every frame with a monotonic timestamp."""

    def __init__(self, account: str):
        super().__init__(daemon=True)
        self.account = account
        self.frames: list[tuple[float, dict]] = []
        self.ready = threading.Event()
        self.failed = None

    def run(self):
        import websocket
        try:
            ws = websocket.create_connection(WS_URL, timeout=20)
            while True:
                frame = json.loads(ws.recv())
                self.frames.append((ms(), frame))
                if frame.get('@system'):
                    status = frame.get('status')
                    if status == 'PENDING_AUTH':
                        ws.send(json.dumps({'key': creds['keyId'], 'secret': creds['secretKey']}))
                    elif status == 'CONNECTED':
                        ws.send(json.dumps({'accountId': self.account,
                                            'subscriptions': ['Order', 'Position']}))
                    elif status in ('FAILED_AUTH', 'TERMINATED', 'INVALID_DATA'):
                        self.failed = f"{status}: {frame.get('message')}"
                        self.ready.set()
                        return
                elif frame.get('requestConfirmed'):
                    self.ready.set()
                ws.settimeout(120)
        except Exception as e:  # noqa: BLE001 — benchmark harness, report and stop
            self.failed = f'{type(e).__name__}: {e}'
            self.ready.set()

    def order_frames(self, client_order_id: str):
        out = []
        for t, f in self.frames:
            o = f.get('order') or {}
            if client_order_id in str(o.get('userOrderId', '')) or \
               o.get('clientOrderId') == client_order_id:
                out.append((t, f))
        return out


def poll_orders_until(account: str, predicate, timeout_s: float, label: str):
    """Poll GET /orders until predicate(order_list) returns a row. -> (ms, row)"""
    deadline = time.monotonic() + timeout_s
    while time.monotonic() < deadline:
        _, status, body = rest('GET', f'/v1/api/accounts/{account}/orders')
        seen = ms()
        rows = body if isinstance(body, list) else []
        hit = predicate(rows)
        if hit:
            return seen, hit
        time.sleep(ORDERS_POLL_INTERVAL)
    print(f'!! timeout waiting for: {label}')
    return None, None


def aapl_safe_limit() -> float:
    """50% below last trade via moomoo OpenD; falls back to $50."""
    try:
        from moomoo import OpenQuoteContext, RET_OK
        ctx = OpenQuoteContext(host='127.0.0.1', port=11111)
        ret, df = ctx.get_market_snapshot(['US.AAPL'])
        ctx.close()
        if ret == RET_OK and len(df):
            last = float(df.iloc[0]['last_price'])
            if last > 1:
                return min(FALLBACK_LIMIT, round(last * 0.5, 2))
    except Exception as e:  # noqa: BLE001
        print(f'moomoo price fetch failed ({e}); using fallback limit')
    return FALLBACK_LIMIT


def main():
    # ---- pre-flight (read-only) ----
    _, status, body = rest('GET', '/v1/api/accounts')
    accounts = body.get('accounts', []) if isinstance(body, dict) else []
    if status != 200 or not accounts:
        sys.exit(f'ABORT: accounts not visible (HTTP {status}, {body}) — '
                 'platform likely outside trading hours. Run during a live session.')
    acct = accounts[0]['account']
    acct_type = accounts[0].get('accountType', '?')
    print(f'account: {acct} ({acct_type})')

    _, _, routes_body = rest('GET', f'/v1/api/accounts/{acct}/routes')
    routes = routes_body.get('routes', []) if isinstance(routes_body, dict) else []
    stock_routes = [r['routeName'] for r in routes if 'Stock' in r.get('securityTypes', [])]
    print(f'stock routes: {stock_routes or "none advertised"}')
    route = 'SMART' if 'SMART' in stock_routes else (stock_routes[0] if stock_routes else None)
    if route is None and acct_type == 'Paper':
        route = ''  # paper auto-assigns
    if route is None:
        sys.exit('ABORT: no Stock route available on this account.')

    limit_price = aapl_safe_limit()
    client_order_id = f'ET-BENCH-{datetime.now(timezone.utc):%Y%m%d%H%M%S}'
    print(f'order: Buy {QTY} {SYMBOL} Limit @{limit_price} Day route={route or "(auto)"} '
          f'id={client_order_id}')

    # ---- WS up BEFORE the order ----
    stream = PortfolioStream(acct)
    stream.start()
    if not stream.ready.wait(30) or stream.failed:
        sys.exit(f'ABORT: portfolio stream not ready: {stream.failed}')
    print(f'{ms():7.0f}ms  WS subscribed')

    # ---- 1. place ----
    order = {'symbol': SYMBOL, 'side': 'Buy', 'openClose': 'Open',
             'orderQuantity': QTY, 'orderType': 'Limit', 'limitPrice': limit_price,
             'timeInForce': 'Day', 'securityType': 'Stock',
             'clientOrderId': client_order_id}
    if route:
        order['route'] = route
    t_post = ms()
    post_ms, post_status, post_body = rest('POST', f'/v1/api/accounts/{acct}/order', order)
    print(f'{ms():7.0f}ms  POST returned: HTTP {post_status} in {post_ms:.0f}ms '
          f'orderStatus={post_body.get("orderStatus") if isinstance(post_body, dict) else post_body}')
    if not (isinstance(post_body, dict) and
            post_body.get('orderStatus') not in (None, 'Rejected')):
        print(f'order NOT working (response: {json.dumps(post_body)[:400]}) — nothing to cancel')
        save_capture(stream, client_order_id, acct)
        return

    # ---- 2. WS ack + 3. REST visibility ----
    t_seen_rest, _ = poll_orders_until(
        acct, lambda rows: next((r for r in rows if r.get('clientOrderId') == client_order_id), None),
        15, 'order visible in GET /orders')
    time.sleep(1.5)  # let any slower WS push land before reading frames
    ws_hits = stream.order_frames(client_order_id)
    t_ws = ws_hits[0][0] if ws_hits else None

    print('\n--- PLACEMENT ---')
    print(f'POST round-trip        : {post_ms:7.0f} ms')
    print(f'POST -> WS push        : {t_ws - t_post:7.0f} ms' if t_ws else 'POST -> WS push        : (no push seen)')
    print(f'POST -> GET /orders    : {t_seen_rest - t_post:7.0f} ms' if t_seen_rest else 'POST -> GET /orders    : (timeout)')

    # ---- 4. cancel ----
    t_del = ms()
    del_ms, del_status, del_body = rest(
        'DELETE', f'/v1/api/accounts/{acct}/orders/{client_order_id}')
    print(f'\n{ms():7.0f}ms  DELETE returned: HTTP {del_status} in {del_ms:.0f}ms')
    if del_status == 404:
        print('DELETE 404 (order too fresh?) — retrying once after 1s')
        time.sleep(1)
        t_del = ms()
        del_ms, del_status, del_body = rest(
            'DELETE', f'/v1/api/accounts/{acct}/orders/{client_order_id}')
        print(f'{ms():7.0f}ms  DELETE retry: HTTP {del_status} in {del_ms:.0f}ms')

    # ---- 5. cancel visibility ----
    def canceled(rows):
        row = next((r for r in rows if r.get('clientOrderId') == client_order_id), None)
        if row is None:
            return {'orderStatus': 'gone-from-open-orders'}
        return row if row.get('orderStatus') in ('Canceled', 'Cancelled') else None
    t_cancel_rest, cancel_row = poll_orders_until(acct, canceled, 15, 'cancel visible')
    time.sleep(1.5)
    cancel_ws = [t for t, f in stream.order_frames(client_order_id)
                 if str((f.get('order') or {}).get('status', '')).lower().startswith('cancel') and t >= t_del]
    t_cancel_ws = cancel_ws[0] if cancel_ws else None

    print('\n--- CANCELLATION ---')
    print(f'DELETE round-trip      : {del_ms:7.0f} ms  (body: {json.dumps(del_body)[:120]})')
    print(f'DELETE -> WS push      : {t_cancel_ws - t_del:7.0f} ms' if t_cancel_ws else 'DELETE -> WS push      : (no push seen)')
    print(f'DELETE -> GET /orders  : {t_cancel_rest - t_del:7.0f} ms  ({cancel_row.get("orderStatus")})' if t_cancel_rest else 'DELETE -> GET /orders  : (timeout)')

    # ---- teardown verification ----
    _, _, rows = rest('GET', f'/v1/api/accounts/{acct}/orders')
    working = [r for r in (rows if isinstance(rows, list) else [])
               if r.get('clientOrderId') == client_order_id and
               r.get('orderStatus') not in ('Canceled', 'Cancelled', 'Rejected', 'Expired', 'Filled')]
    if working:
        print(f'\n!!!! ORDER STILL WORKING: {json.dumps(working)[:300]}')
        print('!!!! CANCEL IT MANUALLY (portal or rerun DELETE) BEFORE THE NEXT SESSION')
    else:
        print('\nclean: no working benchmark orders remain')
    save_capture(stream, client_order_id, acct)


def save_capture(stream, client_order_id, acct):
    CAPTURE_DIR.mkdir(exist_ok=True)
    out = CAPTURE_DIR / f'portfolio_ws_{client_order_id}.json'
    out.write_text(json.dumps(
        {'account': acct, 'clientOrderId': client_order_id,
         'frames': [{'t_ms': round(t, 1), 'frame': f} for t, f in stream.frames]}, indent=1))
    print(f'WS frames captured -> {out}')


if __name__ == '__main__':
    main()
