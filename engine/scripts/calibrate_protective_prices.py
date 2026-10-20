from pathlib import Path
import argparse,bisect,collections,json,sqlite3
ap=argparse.ArgumentParser()
ap.add_argument('segments',nargs='+')
args=ap.parse_args()
def floor(p): return .02 if p>=1 else .0002
def support(b,p):
    lo,hi=b[3],b[4]
    margin=max(floor((lo+hi)/2),hi-lo)
    return lo-margin<=p<=hi+margin
assert floor(.5)==.0002 and floor(1)==.02
assert support((0,0,0,6.30,6.34),6.2708)
assert not support((0,0,0,6.30,6.34),5.23)
for arg in args.segments:
    db=Path(arg).resolve()
    con=sqlite3.connect(db.as_uri()+'?mode=ro',uri=True,timeout=2)
    con.execute('PRAGMA query_only=ON')
    meta=con.execute('SELECT clean,attribution_incomplete,created_ms,closed_ms FROM tickstore_meta').fetchone()
    symbols=[r[0] for r in con.execute("SELECT symbol FROM observations WHERE kind='print' GROUP BY symbol ORDER BY count(*) DESC LIMIT 6")]
    if 'US.SAIQ' not in symbols and '20261020' in db.name: symbols.append('US.SAIQ')
    out=[]
    for symbol in symbols:
        prints=con.execute("""SELECT o.receipt_ms,o.ingress,o.list_index,o.price,o.connection
         FROM observations o JOIN observations p ON p.run_id=o.run_id AND p.connection=o.connection
         AND p.ingress=o.ingress AND p.list_index=o.list_index AND p.kind='processing'
         WHERE o.kind='print' AND o.symbol=? AND o.price>0
         AND json_extract(p.data,'$.Accepted')=1 AND json_extract(p.data,'$.LastEligible')=1
         AND json_extract(p.data,'$.Delivery')=1 AND json_extract(p.data,'$.Seed')=0
         ORDER BY o.receipt_ms,o.ingress,o.list_index LIMIT 20000""",(symbol,)).fetchall()
        if not prints: continue
        quotes=[]
        for t,i,n,cn,payload in con.execute("SELECT receipt_ms,ingress,list_index,connection,data FROM observations WHERE kind='bbo' AND symbol=? AND receipt_ms BETWEEN ? AND ? ORDER BY receipt_ms,ingress,list_index",(symbol,prints[0][0]-1000,prints[-1][0])):
            d=json.loads(payload);bid=(d.get('Bid') or {}).get('Price',0);ask=(d.get('Ask') or {}).get('Price',0)
            clocks=(d.get('BidServerTimestamp'),d.get('AskServerTimestamp'))
            good=bid>0 and ask>=bid and ask-bid<=(bid+ask)*.005 and all(x is not None and x>0 for x in clocks)
            quotes.append((t,i,n,bid,ask,cn,good))
        qkeys=[q[:3] for q in quotes]
        def book(p):
            ix=bisect.bisect_right(qkeys,p[:3])-1
            if ix<0: return None,None
            q=quotes[ix];age=p[0]-q[0]
            return (q if q[6] and q[5]==p[4] and 0<=age<=250 else None),age
        counts=collections.Counter();ages=[];previous=None
        for ix,p in enumerate(prints):
            b,age=book(p)
            if age is not None: ages.append(age)
            counts['prints']+=1
            if b: counts['usable_book']+=1
            jump=previous is not None and abs(p[3]-previous[3])>max(floor(previous[3]),previous[3]*.01)
            outside=b is not None and not support(b,p[3])
            if jump: counts['large_step']+=1
            if outside: counts['outside_book']+=1
            suspect=outside or (jump and not (b and support(b,p[3])))
            if suspect:
                counts['suspect']+=1
                second=False;contrary=False;returning=False
                for q in prints[ix+1:]:
                    if q[0]>=p[0]+100: break
                    qb,_=book(q)
                    if abs(q[3]-p[3])<=max(floor(p[3]),p[3]*.0025):
                        second=True
                        if qb and not support(qb,q[3]): contrary=True
                    if previous and qb and support(qb,previous[3]) and support(qb,q[3]): returning=True
                counts['second_near_in_100ms']+=second
                counts['second_with_contrary_book']+=contrary
                counts['return_with_book_in_100ms']+=returning
            previous=p
        ages.sort()
        out.append({'symbol':symbol,'price_range':[min(p[3] for p in prints),max(p[3] for p in prints)],**counts,'book_age_p50':ages[len(ages)//2] if ages else None,'book_age_p95':ages[int((len(ages)-1)*.95)] if ages else None})
    gaps=con.execute("SELECT count(*) FROM observations WHERE kind='capture_gap'").fetchone()[0]
    con.close()
    print(json.dumps({'segment':db.name,'meta':meta,'gap_markers':gaps,'symbols':out},separators=(',',':')))
