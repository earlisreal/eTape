#!/usr/bin/env python3
"""TZ Portfolio-WS staleness observation (Monday checklist §4, last TZ item).

Connects + auths + subscribes, then passively records EVERY frame — including
WS control frames (ping/pong) — for --secs. Reports: frame-type histogram,
inter-frame gap distribution, longest silence. Read-only; no orders.

The Go adapter needs a staleness threshold: if the server heartbeats every N s,
stale = miss 2-3 of those; if it's silent when idle, staleness must be inferred
from app-level activity or client pings.
"""

import argparse
import json
import time
from datetime import datetime
from pathlib import Path

import websocket
from websocket import ABNF

CAPTURE_DIR = Path(__file__).parent / 'captures'
creds = json.load(open(Path.home() / '.eJournal' / 'credentials.json'))['tradeZero']

OPCODE_NAMES = {ABNF.OPCODE_TEXT: 'text', ABNF.OPCODE_BINARY: 'binary',
                ABNF.OPCODE_PING: 'ping', ABNF.OPCODE_PONG: 'pong',
                ABNF.OPCODE_CLOSE: 'close'}


def main(secs):
    ws = websocket.create_connection('wss://webapi.tradezero.com/stream/portfolio',
                                     timeout=30, fire_cont_frame=False,
                                     skip_utf8_validation=True)
    frames = []       # (t, opcode_name, brief)
    acct = None

    def brief_of(data):
        try:
            f = json.loads(data)
        except (ValueError, UnicodeDecodeError):
            return f'raw:{bytes(data)[:24].hex()}'
        if f.get('@system'):
            return f'system:{f.get("status")}'
        for k in ('order', 'position', 'account'):
            if f.get(k):
                return k
        return ','.join(sorted(f.keys()))[:60]

    t_end = time.time() + secs
    subscribed = False
    while time.time() < t_end:
        try:
            ws.settimeout(min(60, max(1, t_end - time.time())))
            opcode, data = ws.recv_data(control_frame=True)
        except websocket.WebSocketTimeoutException:
            continue
        except Exception as e:  # noqa: BLE001
            frames.append((time.time(), 'error', f'{type(e).__name__}: {e}'))
            break
        name = OPCODE_NAMES.get(opcode, str(opcode))
        brief = brief_of(data) if name in ('text', 'binary') else ''
        frames.append((time.time(), name, brief))
        if name == 'text':
            f = json.loads(data)
            if f.get('@system'):
                st = f.get('status')
                if st == 'PENDING_AUTH':
                    ws.send(json.dumps({'key': creds['keyId'], 'secret': creds['secretKey']}))
                elif st == 'CONNECTED' and not subscribed:
                    import urllib.request
                    req = urllib.request.Request(
                        'https://webapi.tradezero.com/v1/api/accounts',
                        headers={'Accept': 'application/json',
                                 'TZ-API-KEY-ID': creds['keyId'],
                                 'TZ-API-SECRET-KEY': creds['secretKey']})
                    with urllib.request.urlopen(req, timeout=15) as r:
                        acct = json.loads(r.read().decode())['accounts'][0]['account']
                    ws.send(json.dumps({'accountId': acct,
                                        'subscriptions': ['Order', 'Position', 'Account']}))
                    subscribed = True
    ws.close()

    print(f'=== TZ portfolio WS passive observation, {secs}s ===')
    hist = {}
    for _, name, brief in frames:
        key = f'{name}({brief})' if brief else name
        hist[key] = hist.get(key, 0) + 1
    for k, n in sorted(hist.items(), key=lambda kv: -kv[1]):
        print(f'  {n:>5}  {k}')
    ts = [t for t, name, _ in frames if name != 'error']
    gaps = [b - a for a, b in zip(ts, ts[1:])]
    if gaps:
        gaps.sort()
        print(f'\ninter-frame gaps: n={len(gaps)} median={gaps[len(gaps)//2]:.1f}s '
              f'p95={gaps[int(len(gaps)*.95)]:.1f}s max={gaps[-1]:.1f}s')
    tail_silence = time.time() - ts[-1] if ts else secs
    print(f'silence at window end: {tail_silence:.1f}s')

    out = CAPTURE_DIR / f'tz_ws_staleness_{datetime.now():%Y%m%d_%H%M%S}.json'
    out.write_text(json.dumps([{'t': round(t, 3), 'kind': n, 'brief': b}
                               for t, n, b in frames], indent=1))
    print(f'raw -> {out}')


if __name__ == '__main__':
    ap = argparse.ArgumentParser()
    ap.add_argument('--secs', type=int, default=600)
    args = ap.parse_args()
    main(args.secs)
