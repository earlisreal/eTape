#!/usr/bin/env python3
"""Alpaca PAPER side-checks (Monday checklist §4). Read/paper-only, no real money.

  1. TIF acceptance on a standard (non-Elite) account: ioc / fok / opg / cls
     (docs footnote says "contact sales" — verify what POST /v2/orders does)
  2. client_order_id reuse after a terminal state (TZ consumes IDs forever — R114;
     does Alpaca?)
  3. trade_updates stream frame encoding on the PAPER endpoint (binary frames —
     JSON-in-binary or msgpack?)

All orders: buy 1 F, limit far below market (unmarketable), canceled immediately.
"""

import json
import threading
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

BASE = 'https://paper-api.alpaca.markets'
SYMBOL = 'F'
FAR_LIMIT = '5.00'              # F trades ~2x this; never fills
CAPTURE_DIR = Path(__file__).parent / 'captures'

creds = json.load(open(Path.home() / '.eJournal' / 'credentials.json'))['alpaca']
HEADERS = {'APCA-API-KEY-ID': creds['keyId'], 'APCA-API-SECRET-KEY': creds['secretKey'],
           'Accept': 'application/json', 'Content-Type': 'application/json'}


def rest(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, headers=HEADERS, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, json.loads(r.read().decode() or 'null')
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, raw


events = []


def stream():
    import msgpack
    import websocket
    from websocket import ABNF
    try:
        ws = websocket.create_connection(BASE.replace('https', 'wss') + '/stream', timeout=20)
        ws.send(json.dumps({'action': 'auth', 'key': creds['keyId'], 'secret': creds['secretKey']}))
        while True:
            opcode, data = ws.recv_data()
            binary = opcode == ABNF.OPCODE_BINARY
            try:
                parsed, enc = json.loads(data), ('json-in-binary' if binary else 'json-text')
            except (ValueError, UnicodeDecodeError):
                try:
                    parsed, enc = msgpack.unpackb(data), 'msgpack'
                except Exception:  # noqa: BLE001
                    parsed, enc = {'raw_hex': bytes(data)[:80].hex()}, 'unknown-binary'
            events.append({'t': time.time(), 'opcode': int(opcode), 'encoding': enc,
                           'frame': parsed})
            if parsed.get('stream') == 'authorization' and \
               parsed.get('data', {}).get('status') == 'authorized':
                ws.send(json.dumps({'action': 'listen',
                                    'data': {'streams': ['trade_updates']}}))
            ws.settimeout(300)
    except Exception as e:  # noqa: BLE001
        events.append({'error': f'{type(e).__name__}: {e}'})


def place(tif, coid=None, extended=False):
    body = {'symbol': SYMBOL, 'qty': '1', 'side': 'buy', 'type': 'limit',
            'limit_price': FAR_LIMIT, 'time_in_force': tif}
    if coid:
        body['client_order_id'] = coid
    if extended:
        body['extended_hours'] = True
    return rest('POST', '/v2/orders', body)


def main():
    threading.Thread(target=stream, daemon=True).start()
    time.sleep(3)  # let auth/listen frames land

    print('=== 1. TIF acceptance (standard account, paper) ===')
    tif_results = {}
    for tif in ['ioc', 'fok', 'opg', 'cls']:
        status, resp = place(tif)
        oid = resp.get('id') if isinstance(resp, dict) else None
        verdict = f'HTTP {status}'
        if status == 200:
            verdict += f' accepted (status={resp.get("status")})'
            rest('DELETE', f'/v2/orders/{oid}')
        else:
            verdict += f' -> {json.dumps(resp)[:160]}'
        tif_results[tif] = {'status': status, 'resp': resp}
        print(f'  tif={tif:<4}: {verdict}')
        time.sleep(0.4)

    print('\n=== 2. client_order_id reuse after terminal state ===')
    coid = f'et-reuse-{datetime.now(timezone.utc):%H%M%S}'
    s1, r1 = place('day', coid=coid)
    print(f'  first place : HTTP {s1} status={r1.get("status") if isinstance(r1, dict) else r1}')
    oid = r1.get('id')
    rest('DELETE', f'/v2/orders/{oid}')
    time.sleep(1.5)
    _, check = rest('GET', f'/v2/orders/{oid}')
    print(f'  after cancel: status={check.get("status") if isinstance(check, dict) else check}')
    s2, r2 = place('day', coid=coid)
    if s2 == 200:
        print(f'  REUSE ACCEPTED: new order {r2.get("id")} (status={r2.get("status")}) '
              '— unlike TZ R114')
        rest('DELETE', f'/v2/orders/{r2.get("id")}')
    else:
        print(f'  REUSE REJECTED: HTTP {s2} {json.dumps(r2)[:200]}')
    reuse_result = {'first': [s1, r1], 'second': [s2, r2]}

    time.sleep(2)  # let trade_updates for the above orders arrive

    print('\n=== 3. paper stream frame encoding ===')
    encs = {}
    for ev in events:
        if 'frame' in ev:
            encs.setdefault(ev['encoding'], 0)
            encs[ev['encoding']] += 1
    print(f'  frames={len(events)} encodings={encs}')
    tu = [e for e in events if e.get('frame', {}).get('stream') == 'trade_updates']
    print(f'  trade_updates events seen: {len(tu)} '
          f'({[e["frame"]["data"].get("event") for e in tu][:10]})')

    print('\n=== cleanup: cancel-all + verify ===')
    status, _ = rest('DELETE', '/v2/orders')
    _, remaining = rest('GET', '/v2/orders?status=open')
    print(f'  cancel-all HTTP {status}; open orders remaining: {len(remaining)}')

    out = CAPTURE_DIR / f'alpaca_side_checks_{datetime.now():%Y%m%d_%H%M%S}.json'
    out.write_text(json.dumps({'tif': tif_results, 'coid_reuse': reuse_result,
                               'stream_events': events}, indent=1, default=str))
    print(f'raw -> {out}')


if __name__ == '__main__':
    main()
