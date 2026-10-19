# Price Alerts use engine-owned monitoring

Status: accepted design; feature implementation pending.

Price Alerts use durable engine-owned monitoring so changing charts or
Workspaces, or closing every browser window, preserves their conditions and
triggered records. Audio plays once across open Workspaces; triggers recorded
with no open Workspace remain available for silent review, and monitoring
recovery evaluates fresh Last-Eligible Prices without inventing historical
trigger times. This accepts engine/feed availability and subscription costs in
exchange for a lifecycle independent of mounted panels.

Data interruptions recover automatically on fresh eligible input, while an
explicitly Paused alert stays paused. An enabled alert awaiting subscription
capacity remains saved in a visible `Waiting for data capacity` state; alert
demand preserves existing execution/chart subscriptions. Saving an alert does
not imply that monitoring has started.

The [approved specification](../../.scratch/price-alerts/spec.md) records the
interface and lifecycle decisions.
