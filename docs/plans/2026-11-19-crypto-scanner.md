# Crypto Scanner and Following

Add a separate Crypto Scanner panel and an independent Crypto Monitoring
workspace. The stock Monitoring workspace and Crypto Monitoring each own one
selected source and can follow simultaneously.

- Use the existing perpetual feed router and exact exchange contract IDs.
  Share bulk ticker snapshots and the five-minute history per exchange.
- Provide per-panel ranking, filters, sort and sound. Reuse the chart assignment
  planner for pinned charts and keep the last assignment when rankings are stale.
- Persist the default discovery exchange and a preferred configured account
  independently. Apply account selections only through explicit Link Group actions.
- Cover the rolling baseline with engine fixtures and exercise simultaneous
  Following, toggle persistence, and saved layout loading in the UI.
- Market data is available for MEXC, Binance and BingX. Execution follows each
  adapter's existing capabilities. Disable Following from the Crypto Scanner to
  stop assignments and close all Crypto Scanners to release snapshot demand.

See the [UI guide](../../ui/README.md) and the [Scanner engine guide](../../engine/internal/scan/README.md).
