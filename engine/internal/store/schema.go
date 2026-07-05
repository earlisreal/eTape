package store

// schemaSQL is the Plan 3 schema (market-data plane). Plan 4 adds
// exec_events/fills. All timestamps are epoch milliseconds (INTEGER),
// matching the domain's TsMs/BucketMs int64 fields.
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
`
