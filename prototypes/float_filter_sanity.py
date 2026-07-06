#!/usr/bin/env python3
"""Qot_StockFilter FLOAT_SHARE sanity check (Monday checklist §1, item 3).

Pulls the low-float universe the scanner warm-up would use — float <= 50M shares
(FLOAT_SHARE unit is THOUSANDS -> filter_max=50_000), price $0.5-$50 — then
cross-checks a sample against get_market_snapshot's outstanding_shares
(= true free float, DJT-verified 2026-07-03). Flags unit mismatches.
"""

import json
import time
from datetime import datetime
from pathlib import Path

from moomoo import Market, OpenQuoteContext, RET_OK, SimpleFilter, StockField

CAPTURE_DIR = Path(__file__).parent / 'captures'
FLOAT_MAX_TH = 50_000        # 50M shares, in THOUSANDS
PRICE_MIN, PRICE_MAX = 0.5, 50.0
PAGES = 3                    # 3 x 200 rows is plenty for a sanity check


def main():
    ctx = OpenQuoteContext(host='127.0.0.1', port=11111)
    try:
        f_float = SimpleFilter()
        f_float.stock_field = StockField.FLOAT_SHARE
        f_float.filter_max = FLOAT_MAX_TH
        f_float.is_no_filter = False
        f_price = SimpleFilter()
        f_price.stock_field = StockField.CUR_PRICE
        f_price.filter_min = PRICE_MIN
        f_price.filter_max = PRICE_MAX
        f_price.is_no_filter = False

        rows, begin, last_page = [], 0, False
        t0 = time.monotonic()
        for page in range(PAGES):
            ret, data = ctx.get_stock_filter(market=Market.US,
                                             filter_list=[f_float, f_price],
                                             begin=begin, num=200)
            if ret != RET_OK:
                print(f'filter error page {page}: {data}')
                break
            last_page, all_count, page_rows = data
            rows.extend(page_rows)
            begin += len(page_rows)
            print(f'  page {page}: {len(page_rows)} rows (all_count={all_count})')
            if last_page:
                break
            time.sleep(3.2)   # 10 req/30s limit
        elapsed = time.monotonic() - t0
        print(f'universe fetch: {len(rows)} rows in {elapsed:.1f}s (all_count={all_count})')

        # cross-check a sample against snapshot free float (one-by-one: batches
        # containing OTC codes fail wholesale — known caveat from 2026-07-03)
        step = max(1, len(rows) // 20)
        sample_rows = rows[::step][:20]
        checks, bad = [], 0
        for r in sample_rows:
            ret, snap = ctx.get_market_snapshot([r.stock_code])
            if ret != RET_OK:
                print(f'  {r.stock_code:<12} snapshot error: {str(snap)[:80]}')
                time.sleep(0.6)
                continue
            try:
                filt_float_sh = r[StockField.FLOAT_SHARE] * 1000  # thousands -> shares
            except Exception as e:  # noqa: BLE001
                print(f'  {r.stock_code:<12} filter field read failed: {e}')
                filt_float_sh = None
            snap_float = snap.iloc[0].get('outstanding_shares')
            ratio = (filt_float_sh / snap_float) if (filt_float_sh and snap_float) else None
            ok = ratio is not None and 0.5 <= ratio <= 2.0
            bad += 0 if ok else 1
            checks.append({'code': r.stock_code, 'filter_float_sh': filt_float_sh,
                           'snapshot_outstanding': snap_float,
                           'ratio': round(ratio, 3) if ratio else None, 'ok': ok})
            print(f'  {r.stock_code:<12} filter={filt_float_sh!s:>14} '
                  f'snapshot={snap_float!s:>14} ratio={ratio if ratio is None else round(ratio, 2)}')
            time.sleep(0.6)

        print(f'\nverdict: {len(checks)} cross-checked, {bad} outside 0.5x-2x '
              f'({"UNIT LOOKS RIGHT" if checks and bad <= len(checks) // 4 else "CHECK UNITS/FIELDS"})')
        out = CAPTURE_DIR / f'float_filter_sanity_{datetime.now():%Y%m%d_%H%M%S}.json'
        out.write_text(json.dumps({
            'all_count': all_count, 'fetched': len(rows), 'elapsed_s': round(elapsed, 1),
            'checks': checks,
            'first_rows': [{'code': r.stock_code, 'name': r.stock_name} for r in rows[:50]],
        }, indent=1, default=str))
        print(f'raw -> {out}')
    finally:
        ctx.close()


if __name__ == '__main__':
    main()
