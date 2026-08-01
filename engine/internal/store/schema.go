package store

// schemaSQL is eTape's SQLite schema for archived bars, config docs,
// sys_events, and execution persistence. All timestamps are epoch
// milliseconds (INTEGER), matching the domain's TsMs/BucketMs int64 fields.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS bars_10s (
  symbol TEXT NOT NULL, ts INTEGER NOT NULL,
  o REAL, h REAL, l REAL, c REAL, v INTEGER,
  PRIMARY KEY (symbol, ts)
);
CREATE TABLE IF NOT EXISTS bars_1m (
  symbol TEXT NOT NULL, ts INTEGER NOT NULL,
  o REAL, h REAL, l REAL, c REAL, v INTEGER,
  PRIMARY KEY (symbol, ts)
);
CREATE TABLE IF NOT EXISTS bars_daily (
  symbol TEXT NOT NULL, ts INTEGER NOT NULL,
  o REAL, h REAL, l REAL, c REAL, v INTEGER,
  PRIMARY KEY (symbol, ts)
);
CREATE TABLE IF NOT EXISTS config (
  key TEXT PRIMARY KEY, value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sys_events (
  seq    INTEGER PRIMARY KEY AUTOINCREMENT,
  ts     INTEGER NOT NULL,
  kind   TEXT    NOT NULL,
  detail TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS exec_events (
  seq      INTEGER PRIMARY KEY AUTOINCREMENT,
  ts       INTEGER NOT NULL,
  source   TEXT    NOT NULL,
  venue    TEXT    NOT NULL,
  type     TEXT    NOT NULL,
  order_id TEXT    NOT NULL,
  payload  TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_exec_events_ts ON exec_events(ts);
CREATE TABLE IF NOT EXISTS fills (
  fill_id  INTEGER PRIMARY KEY AUTOINCREMENT,
  seq      INTEGER NOT NULL REFERENCES exec_events(seq),
  order_id TEXT    NOT NULL,
  symbol   TEXT    NOT NULL,
  side     TEXT    NOT NULL,
  qty      REAL    NOT NULL,
  price    REAL    NOT NULL,
  ts       INTEGER NOT NULL,
  venue    TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_fills_symbol_ts ON fills(symbol, ts);
`
