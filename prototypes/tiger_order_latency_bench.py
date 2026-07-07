#!/usr/bin/env python3
"""Tiger Brokers order-latency benchmark — mirrors venue_order_latency_bench.py.

Standalone (own interpreter/venv) because tigeropen pulls its own
pandas/protobuf/numpy that would fight the moomoo SDK in the shared pyenv, and
because Tiger has its own market data — no need for moomoo's LV3 NBBO. This is
the same shape the per-broker scripts (tz_/moomoo_) had before they were merged.

Two modes, auto-selected from the account's quote entitlements:

  ack-only  (no US real-time data)   — the current reality on Earl's tiger_id:
            place a deliberately NON-marketable BUY limit (ref*0.5, well below
            the market), measure place->api-return and place->order-push ack,
            then cancel. Never fills, needs no live NBBO. Works any session.
            This is the only Tiger latency number obtainable without buying the
            paid US real-time add-on.

  fill      (US real-time entitlement present) — marketable 1-share limit from
            Tiger's own get_stock_briefs NBBO -> ack -> fill push -> flatten,
            exactly like the other-venue benchmark. Auto-enabled iff a
            us*Quote* permission shows up in get_quote_permission().

Safety (same rules as the merged bench + CLAUDE.md):
  - demo (paper) config by default; NO real money.
  - --config live requires --live-go AND (fill mode) an allowed session window,
    1 share, long only, marketable LIMIT (never market), price guard, flatten +
    verify flat. Re-confirm authorization in the running conversation.
  - captures hold account ids -> default capture dir is /tmp, never the public repo.

Usage (venv with tigeropen):
  PY=/tmp/tiger_bench_venv/bin/python
  $PY tiger_order_latency_bench.py                          # demo, ack-only, auto session
  $PY tiger_order_latency_bench.py --cycles 3 --symbol F
  $PY tiger_order_latency_bench.py --config live --live-go  # only with authorization + data
"""
import argparse
import shutil
import sys
import tempfile
import threading
import time
from datetime import datetime
from pathlib import Path
from zoneinfo import ZoneInfo

from tigeropen.tiger_open_config import TigerOpenClientConfig
from tigeropen.common.consts import Market
from tigeropen.common.util.contract_utils import stock_contract
from tigeropen.common.util.order_utils import limit_order
from tigeropen.quote.quote_client import QuoteClient
from tigeropen.trade.trade_client import TradeClient
from tigeropen.push.push_client import PushClient

ET = ZoneInfo('America/New_York')
PRICE_GUARD = 30.0
SPREAD_GUARD = 0.02
BUY_BUFFER_MIN = 0.03
BUY_BUFFER_PCT = 0.002

T0 = time.monotonic()
def ms() -> float:
    return (time.monotonic() - T0) * 1000


def buffer(px: float) -> float:
    return max(BUY_BUFFER_MIN, round(px * BUY_BUFFER_PCT, 2))


def et_session():
    now = datetime.now(ET)
    if now.weekday() >= 5:
        return 'closed'
    hm = (now.hour, now.minute)
    if (9, 30) <= hm < (16, 0):
        return 'rth'
    if (4, 0) <= hm < (9, 30) or (16, 0) <= hm < (20, 0):
        return 'eth'          # pre / post
    return 'overnight'        # 20:00-04:00


def load_config(path):
    """SDK reads a directory containing tiger_openapi_config.properties. Stage
    the chosen (demo/live) file into a temp dir under that canonical name."""
    d = tempfile.mkdtemp(prefix='tiger_cfg_')
    shutil.copy(path, Path(d) / 'tiger_openapi_config.properties')
    return TigerOpenClientConfig(props_path=d)


class Timeline:
    """One order's latency milestones (monotonic ms), same schema as the merged bench."""
    def __init__(self, side, cycle, cold):
        self.d = {'venue': 'tiger', 'side': side, 'cycle': cycle, 'cold': cold,
                  't_send': None, 'api_return_ms': None, 'ack_ms': None,
                  'fill_ms': None, 'order_id': None, 'fill_price': None,
                  'status': None, 'note': None}

    def send(self):
        self.d['t_send'] = ms()

    def mark(self, key, t=None):
        if self.d.get(key) is None and self.d['t_send'] is not None:
            self.d[key] = round((t if t is not None else ms()) - self.d['t_send'], 1)

    def row(self):
        return self.d

    def __str__(self):
        d = self.d
        return (f"tiger {d['side']:<4} c{d['cycle']}{'*' if d['cold'] else ' '} "
                f"api={str(d['api_return_ms']) or '?':>7}  ack={str(d['ack_ms'] or '—'):>7}  "
                f"fill={str(d['fill_ms'] or '—'):>7}  st={d['status'] or '—'}  px={d['fill_price'] or '—'}")


class TigerBench:
    def __init__(self, cfg, symbol, capture_dir):
        self.cfg = cfg
        self.symbol = symbol
        self.account = cfg.account
        self.capture_dir = capture_dir
        self.quote = QuoteClient(cfg)
        self.trade = TradeClient(cfg)
        self.push = None
        self.order_frames = []        # (ms, frame_repr, id, status, filled, avg)
        self.txn_frames = []          # (ms, frame_repr, order_id, filled_price, filled_qty)
        self.has_us_realtime = False
        self.ref_price = None

    # ---- entitlements / reference price ----
    def check_entitlements(self):
        perms = self.quote.get_quote_permission()
        names = [p.get('name') for p in perms]
        self.has_us_realtime = any(str(n).startswith('us') and 'Quote' in str(n) for n in names)
        print(f'  quote permissions: {names}')
        print(f'  US real-time entitlement: {self.has_us_realtime}  -> mode='
              f'{"fill" if self.has_us_realtime else "ack-only"}')

    def nbbo(self):
        """Real-time bid/ask/last (fill mode only)."""
        df = self.quote.get_stock_briefs([self.symbol], include_hour_trading=True)
        row = df.iloc[0]
        return float(row['bid_price']), float(row['ask_price']), float(row['latest_price'])

    def ref_from_delayed(self):
        """Ack-only mode: a rough reference from free 15-min-delayed briefs, only
        to pick a limit far below market. Never used as a tradeable price."""
        df = self.quote.get_stock_delay_briefs([self.symbol])
        row = df.iloc[0]
        ref = row.get('pre_close') or row.get('close') or row.get('latest_price')
        self.ref_price = float(ref)
        return self.ref_price

    # ---- push wiring ----
    def connect(self):
        assets = self.trade.get_prime_assets(base_currency='USD')
        print(f'  account {str(self.account)[-4:]} (…), segments '
              f'{list(assets.segments.keys()) if hasattr(assets, "segments") else "?"}')
        proto, host, port = self.cfg.socket_host_port
        self.push = PushClient(host, port, use_ssl=(proto == 'ssl'))
        self.push.order_changed = self._on_order
        self.push.transaction_changed = self._on_txn
        self.push.error_callback = lambda f: print(f'  push error: {f}')
        self.push.connect(self.cfg.tiger_id, self.cfg.private_key)
        self.push.subscribe_order(account=self.account)
        self.push.subscribe_transaction(account=self.account)
        time.sleep(1.0)          # let subscriptions land
        print('  push connected; order + transaction subscribed')

    def _on_order(self, frame):
        try:
            fid = str(getattr(frame, 'id', ''))
            status = str(getattr(frame, 'status', ''))
            filled = getattr(frame, 'filledQuantity', None)
            avg = getattr(frame, 'avgFillPrice', None)
            self.order_frames.append((ms(), str(frame).replace('\n', ' '), fid, status, filled, avg))
        except Exception as e:  # noqa: BLE001
            self.order_frames.append((ms(), f'PARSE_ERR {e}', '', '', None, None))

    def _on_txn(self, frame):
        try:
            oid = str(getattr(frame, 'orderId', ''))
            fp = getattr(frame, 'filledPrice', None)
            fq = getattr(frame, 'filledQuantity', None)
            self.txn_frames.append((ms(), str(frame).replace('\n', ' '), oid, fp, fq))
        except Exception as e:  # noqa: BLE001
            self.txn_frames.append((ms(), f'PARSE_ERR {e}', '', None, None))

    # ---- order lifecycle ----
    def _watch(self, global_id, tl, want_fill, timeout=25):
        deadline = time.monotonic() + timeout
        gid = str(global_id)
        while time.monotonic() < deadline:
            for t, _, fid, status, filled, avg in list(self.order_frames):
                if fid != gid:
                    continue
                tl.mark('ack_ms', t)
                tl.d['status'] = status
                if status in ('FILLED', 'Filled') or (filled and str(filled) not in ('0', 'None')):
                    tl.mark('fill_ms', t)
                    tl.d['fill_price'] = avg
                    return True
            for t, _, oid, fp, fq in list(self.txn_frames):
                if oid == gid:
                    tl.mark('fill_ms', t)
                    tl.d['fill_price'] = fp
                    return True
            if not want_fill and tl.d['ack_ms'] is not None:
                return True          # ack-only: done as soon as we see the ack
            time.sleep(0.02)
        return False

    def order(self, side, limit_price, cycle, cold, want_fill, session):
        tl = Timeline(side, cycle, cold)
        contract = stock_contract(symbol=self.symbol, currency='USD')
        o = limit_order(self.account, contract, 'BUY' if side == 'buy' else 'SELL',
                        1, round(limit_price, 2))
        o.user_mark = f'ET-BENCH-{side}-{cycle}'
        if session in ('eth', 'overnight'):
            o.outside_rth = True
        if session == 'overnight':
            o.trading_session_type = 'OVERNIGHT'
        tl.send()
        try:
            gid = self.trade.place_order(o)
            tl.mark('api_return_ms')
        except Exception as e:  # noqa: BLE001
            tl.mark('api_return_ms')
            tl.d['status'] = 'PLACE_ERROR'
            tl.d['note'] = f'{type(e).__name__}: {e}'
            print(f'  !! place_order error: {tl.d["note"]}')
            return tl, False
        tl.d['order_id'] = str(gid or o.id)
        done = self._watch(gid or o.id, tl, want_fill)
        # always cancel a resting/unfilled order
        if tl.d['fill_ms'] is None:
            try:
                self.trade.cancel_order(account=self.account, id=(gid or o.id))
            except Exception as e:  # noqa: BLE001
                print(f'  !! cancel error: {e}')
        return tl, done

    def sweep_orphans(self):
        try:
            oo = self.trade.get_open_orders(account=self.account)
        except Exception as e:  # noqa: BLE001
            print(f'  sweep: get_open_orders error {e}')
            return
        for o in oo:
            if str(getattr(o, 'user_mark', '') or '').startswith('ET-BENCH'):
                print(f'  !! orphaned bench order id={o.id} status={o.status} — cancelling')
                try:
                    self.trade.cancel_order(account=self.account, id=o.id)
                except Exception as e:  # noqa: BLE001
                    print(f'    cancel error: {e}')

    def position_qty(self):
        try:
            pos = self.trade.get_positions(account=self.account, symbol=self.symbol)
        except Exception:  # noqa: BLE001
            return 0.0
        for p in pos:
            if getattr(p.contract, 'symbol', None) == self.symbol:
                return float(p.quantity)
        return 0.0

    def teardown(self):
        stamp = datetime.now().strftime('%Y%m%d_%H%M%S')
        out = Path(self.capture_dir) / f'tiger_bench_pushes_{stamp}.json'
        import json
        out.write_text(json.dumps({
            'account_suffix': str(self.account)[-4:],
            'order_frames': [{'t_ms': round(t, 1), 'id': i, 'status': s,
                              'filled': str(f), 'avg': str(a), 'raw': r}
                             for t, r, i, s, f, a in self.order_frames],
            'txn_frames': [{'t_ms': round(t, 1), 'order_id': o, 'filled_price': str(fp),
                            'filled_qty': str(fq), 'raw': r}
                           for t, r, o, fp, fq in self.txn_frames],
        }, indent=1, default=str))
        print(f'  pushes -> {out}')
        try:
            if self.push:
                self.push.disconnect()
        except Exception:  # noqa: BLE001
            pass


def main():
    global PRICE_GUARD
    ap = argparse.ArgumentParser()
    ap.add_argument('--config', choices=['demo', 'live'], default='demo')
    ap.add_argument('--symbol', default='F')
    ap.add_argument('--cycles', type=int, default=3)
    ap.add_argument('--session', choices=['auto', 'rth', 'eth', 'overnight'], default='auto')
    ap.add_argument('--live-go', action='store_true')
    ap.add_argument('--price-guard', type=float, default=PRICE_GUARD)
    ap.add_argument('--capture-dir', default='/tmp/tiger_captures')
    args = ap.parse_args()
    PRICE_GUARD = args.price_guard
    symbol = args.symbol.upper()

    cfg_path = str(Path.home() / 'Documents' / f'tiger_openapi_config_{args.config}.properties')
    session = et_session() if args.session == 'auto' else args.session
    print(f'config={args.config} symbol={symbol} session={session} '
          f'ET={datetime.now(ET):%Y-%m-%d %H:%M:%S} cycles={args.cycles}')

    if args.config == 'live' and not args.live_go:
        sys.exit('ABORT: live config requires --live-go (Earl\'s explicit in-conversation authorization)')

    Path(args.capture_dir).mkdir(parents=True, exist_ok=True)
    cfg = load_config(cfg_path)
    bench = TigerBench(cfg, symbol, args.capture_dir)
    bench.check_entitlements()
    want_fill = bench.has_us_realtime

    if args.config == 'live' and want_fill and session == 'closed':
        sys.exit('ABORT: live fill benchmark outside any trading session')

    timelines = []
    try:
        bench.connect()
        bench.sweep_orphans()
        start_qty = bench.position_qty()
        if start_qty != 0:
            sys.exit(f'ABORT: pre-existing {symbol} position qty={start_qty}')

        for cycle in range(1, args.cycles + 1):
            if want_fill:
                bid, ask, last = bench.nbbo()
                if not (0 < bid < ask) or last > PRICE_GUARD:
                    print(f'  !! guard: bid={bid} ask={ask} last={last} — skip cycle')
                    continue
                spread = (ask - bid) / ((ask + bid) / 2)
                if spread > SPREAD_GUARD:
                    print(f'  !! spread {spread:.2%} > {SPREAD_GUARD:.0%} — skip cycle')
                    continue
                buy_px = ask + buffer(ask)
            else:
                ref = bench.ref_price or bench.ref_from_delayed()
                buy_px = round(ref * 0.5, 2)     # deliberately non-marketable
            tl, ok = bench.order('buy', buy_px, cycle, cold=(cycle == 1),
                                 want_fill=want_fill, session=session)
            timelines.append(tl.row())
            print(f'  {tl}' + (f'  ({tl.d["note"]})' if tl.d.get('note') else ''))

            if want_fill and ok and tl.d['fill_ms'] is not None:
                time.sleep(0.5)
                bid, ask, _ = bench.nbbo()
                sell_px = max(bid - buffer(bid), 0.01)
                stl, sok = bench.order('sell', sell_px, cycle, cold=False,
                                       want_fill=True, session=session)
                timelines.append(stl.row())
                print(f'  {stl}')
            time.sleep(0.8)

        qty = bench.position_qty()
        print(f'\n  final {symbol} position qty={qty} '
              f'{"— FLAT" if qty == 0 else "!!!! NOT FLAT — FLATTEN NOW"}')
    finally:
        bench.teardown()

    print('\n=== SUMMARY (ms; * = cold) ===')
    print(f'{"side":<6}{"cyc":<5}{"api-return":>11}{"ack-push":>10}{"fill-push":>10}{"status":>16}')
    for r in timelines:
        print(f'{r["side"]:<6}{r["cycle"]}{"*" if r["cold"] else " ":<4}'
              f'{str(r["api_return_ms"] or "—"):>11}{str(r["ack_ms"] or "—"):>10}'
              f'{str(r["fill_ms"] or "—"):>10}{str(r["status"] or "—"):>16}')
    acks = [r['ack_ms'] for r in timelines if r['ack_ms'] is not None]
    apis = [r['api_return_ms'] for r in timelines if r['api_return_ms'] is not None]
    if apis:
        print(f'\napi-return: min={min(apis):.0f} max={max(apis):.0f} n={len(apis)}')
    if acks:
        print(f'ack-push:   min={min(acks):.0f} max={max(acks):.0f} n={len(acks)}')


if __name__ == '__main__':
    main()
