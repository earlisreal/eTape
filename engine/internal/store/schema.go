package store

// schemaSQL is the Plan 3 schema (market-data plane) plus the Plan 4
// exec_events/fills tables (execution log). All timestamps are epoch
// milliseconds (INTEGER), matching the domain's TsMs/BucketMs int64 fields.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS journal (
  day     TEXT    NOT NULL,   -- ET trading day, "YYYY-MM-DD"
  seq     INTEGER NOT NULL,   -- per-day monotonic, arrival order
  ts_exch INTEGER NOT NULL,   -- event exchange ts (ms); ts_recv for conn/resync events
  ts_recv INTEGER NOT NULL,   -- pipeline receive ts (ms) — metadata only
  symbol  TEXT    NOT NULL,   -- "" for conn/resync events
  kind    TEXT    NOT NULL,   -- ticks|quote|book|bars1m|conn_up|conn_down|resynced
  seed    INTEGER NOT NULL,   -- 0/1: feed.Event Seed flag
  payload TEXT    NOT NULL,   -- JSON of the whole feed.Event struct
  PRIMARY KEY (day, seq)
);
CREATE TABLE IF NOT EXISTS journal_chunks (
  day       TEXT    NOT NULL,   -- ET trading day, same domain as journal.day
  chunk_no  INTEGER NOT NULL,   -- 0-based within the day
  first_seq INTEGER NOT NULL,
  last_seq  INTEGER NOT NULL,
  n_rows    INTEGER NOT NULL,
  body      BLOB    NOT NULL,   -- zstd frame of JSONL-encoded rows
  PRIMARY KEY (day, chunk_no)
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
  kind   TEXT NOT NULL,
  detail TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS exec_events (
  seq      INTEGER PRIMARY KEY AUTOINCREMENT,
  ts       INTEGER NOT NULL,          -- event ts (epoch ms)
  source   TEXT    NOT NULL,          -- local|ws|rest|reconcile
  venue    TEXT    NOT NULL,
  type     TEXT    NOT NULL,          -- event kind, e.g. order_submitted
  order_id TEXT    NOT NULL,          -- "" for stream_gap
  payload  TEXT    NOT NULL           -- JSON of the concrete event
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
