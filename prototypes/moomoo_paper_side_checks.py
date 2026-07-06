#!/usr/bin/env python3
"""moomoo side-checks (Monday checklist §4). Paper orders only; REAL is read-only.

  1. Trd_GetFunds (accinfo_query) shape — day-P&L candidate fields + USD cash
     info on BOTH the US paper margin acc (STOCK_AND_OPTION, refresh_cache=True)
     and the REAL FUTUSG universal account (read-only) — feeds gate rule 5.
  2. Do order pushes arrive on the US paper account? (skill docs warn pushes
     "may not be received temporarily" there.) Also: fill_outside_rth acceptance
     on paper (the "paper ETH contradiction").
  3. Order.remark echo on order pushes + order_list_query (fill-side echo needs
     an RTH fill — deferred to the benchmark).
  4. cancel-all: synchronous ack vs per-order pushes only.
"""

import json
import time
from datetime import datetime
from pathlib import Path

from moomoo import (Currency, OpenSecTradeContext, OrderType, RET_OK, SecurityFirm,
                    TradeDealHandlerBase, TradeOrderHandlerBase, TrdEnv, TrdMarket, TrdSide)

CAPTURE_DIR = Path(__file__).parent / 'captures'
SYMBOL = 'US.F'
FAR_LIMIT = 9.00          # F ~ $12; never fills

pushes = []


def main():
    results = {}
    bench_t0 = time.monotonic()

    class OrderPush(TradeOrderHandlerBase):
        def on_recv_rsp(self, rsp_pb):
            ret, data = super().on_recv_rsp(rsp_pb)
            if ret == RET_OK and len(data):
                for rec in data.to_dict('records'):
                    pushes.append({'t': round((time.monotonic() - bench_t0) * 1000, 1),
                                   'kind': 'order', 'rec': rec})
            return ret, data

    class DealPush(TradeDealHandlerBase):
        def on_recv_rsp(self, rsp_pb):
            ret, data = super().on_recv_rsp(rsp_pb)
            if ret == RET_OK and len(data):
                for rec in data.to_dict('records'):
                    pushes.append({'t': round((time.monotonic() - bench_t0) * 1000, 1),
                                   'kind': 'deal', 'rec': rec})
            return ret, data

    ctx = OpenSecTradeContext(filter_trdmarket=TrdMarket.NONE, host='127.0.0.1',
                              port=11111, security_firm=SecurityFirm.FUTUSG)
    ctx.set_handler(OrderPush())
    ctx.set_handler(DealPush())
    try:
        ret, df = ctx.get_acc_list()
        assert ret == RET_OK, df
        accs = df.to_dict('records')
        paper_us = next(a for a in accs if a['trd_env'] == 'SIMULATE'
                        and 'US' in (a.get('trdmarket_auth') or [])
                        and a.get('acc_type') in ('MARGIN', 'STOCK_AND_OPTION'))
        real = next(a for a in accs if a['trd_env'] == 'REAL'
                    and 'US' in (a.get('trdmarket_auth') or [])
                    and a.get('acc_role') != 'MASTER')
        print(f"paper US acc: {paper_us['acc_id']} ({paper_us.get('acc_type')}, "
              f"sim_acc_type={paper_us.get('sim_acc_type')})")
        print(f"real acc    : {real['acc_id']} ({real.get('acc_type')}) — read-only here")
        results['accounts'] = {'paper_us': paper_us, 'real': real}

        # ---- 1. funds shape --------------------------------------------
        print('\n=== 1. accinfo_query (Trd_GetFunds) shapes ===')
        for label, acc, env, kwargs in [
                ('paper-US', paper_us['acc_id'], TrdEnv.SIMULATE, {'refresh_cache': True}),
                ('REAL-universal', real['acc_id'], TrdEnv.REAL, {}),
                ('REAL-universal-USD', real['acc_id'], TrdEnv.REAL, {'currency': Currency.USD})]:
            ret, fdf = ctx.accinfo_query(trd_env=env, acc_id=acc, **kwargs)
            if ret != RET_OK:
                print(f'  {label}: ERROR {fdf}')
                results[f'funds_{label}'] = {'error': str(fdf)}
                continue
            row = fdf.to_dict('records')[0]
            pl_fields = {k: v for k, v in row.items()
                         if any(s in k.lower() for s in
                                ('pl', 'pnl', 'profit', 'today', 'cash', 'currency', 'power',
                                 'assets', 'val'))}
            print(f'  {label}: {len(row)} cols; P&L/cash-ish fields:')
            print(f'    {json.dumps(pl_fields, default=str)[:600]}')
            results[f'funds_{label}'] = row

        # ---- 2+3. paper orders: pushes, ETH flag, remark echo -----------
        print('\n=== 2/3. paper orders: pushes + fill_outside_rth + remark ===')
        order_ids = []
        for i, outside_rth in enumerate([False, True]):
            t0 = time.monotonic()
            ret, odf = ctx.place_order(price=FAR_LIMIT, qty=1, code=SYMBOL,
                                       trd_side=TrdSide.BUY, order_type=OrderType.NORMAL,
                                       trd_env=TrdEnv.SIMULATE, acc_id=paper_us['acc_id'],
                                       remark=f'ET-CHECK-{i}',
                                       fill_outside_rth=outside_rth)
            rtt = (time.monotonic() - t0) * 1000
            if ret != RET_OK:
                print(f'  place {i} (fill_outside_rth={outside_rth}): ERROR {odf}')
                results[f'place_{i}'] = {'error': str(odf)}
            else:
                rec = odf.to_dict('records')[0]
                order_ids.append(rec['order_id'])
                print(f'  place {i} (fill_outside_rth={outside_rth}): ok {rtt:.0f}ms '
                      f'order_id={rec["order_id"]} status={rec.get("order_status")}')
                results[f'place_{i}'] = rec
            time.sleep(0.3)

        time.sleep(5)   # push observation window
        order_pushes = [p for p in pushes if p['kind'] == 'order']
        print(f'  order pushes within 5s: {len(order_pushes)}')
        for p in order_pushes:
            r = p['rec']
            print(f"    t={p['t']}ms {r.get('order_id')} {r.get('order_status')} "
                  f"remark={r.get('remark')!r}")

        ret, ldf = ctx.order_list_query(trd_env=TrdEnv.SIMULATE, acc_id=paper_us['acc_id'],
                                        refresh_cache=True)
        rows = ldf.to_dict('records') if ret == RET_OK else []
        mine = [r for r in rows if str(r.get('order_id')) in {str(o) for o in order_ids}]
        print(f'  order_list_query: {len(mine)} bench orders; '
              f'remarks={[r.get("remark") for r in mine]}')
        results['order_list'] = mine

        # ---- 4. cancel-all ----------------------------------------------
        print('\n=== 4. cancel-all semantics ===')
        n_before = len(pushes)
        t0 = time.monotonic()
        if hasattr(ctx, 'cancel_all_order'):
            ret, cdf = ctx.cancel_all_order(trd_env=TrdEnv.SIMULATE,
                                            acc_id=paper_us['acc_id'],
                                            trdmarket=TrdMarket.US)
            rtt = (time.monotonic() - t0) * 1000
            print(f'  cancel_all_order returned in {rtt:.0f}ms: ret={ret} '
                  f'body={str(cdf)[:200]!r}')
            results['cancel_all'] = {'rtt_ms': round(rtt, 1), 'ret': int(ret),
                                     'body': str(cdf)[:500]}
        else:
            print('  SDK lacks cancel_all_order — cancelling individually')
            from moomoo import ModifyOrderOp
            for oid in order_ids:
                ctx.modify_order(ModifyOrderOp.CANCEL, oid, 0, 0,
                                 trd_env=TrdEnv.SIMULATE, acc_id=paper_us['acc_id'])
        time.sleep(5)
        cancel_pushes = [p for p in pushes[n_before:] if p['kind'] == 'order']
        print(f'  per-order pushes after cancel-all: {len(cancel_pushes)} '
              f'({[p["rec"].get("order_status") for p in cancel_pushes]})')

        ret, ldf = ctx.order_list_query(trd_env=TrdEnv.SIMULATE, acc_id=paper_us['acc_id'],
                                        refresh_cache=True)
        open_left = [r for r in (ldf.to_dict('records') if ret == RET_OK else [])
                     if r.get('order_status') in ('SUBMITTED', 'SUBMITTING', 'WAITING_SUBMIT')]
        print(f'  open orders remaining: {len(open_left)}')
        results['pushes'] = pushes

        out = CAPTURE_DIR / f'moomoo_paper_side_checks_{datetime.now():%Y%m%d_%H%M%S}.json'
        out.write_text(json.dumps(results, indent=1, default=str))
        print(f'\nraw -> {out}')
    finally:
        ctx.close()


if __name__ == '__main__':
    main()
