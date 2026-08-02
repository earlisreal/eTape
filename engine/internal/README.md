# Internal Packages

Private engine implementation. `feed/opend` normalizes input, `md` builds state, `store` persists, scanners enrich, `exec` gates orders, broker adapters execute, `uihub` bridges UI.

Groups: [broker](broker/README.md), [feed](feed/README.md), [history](hist/README.md), [market data](md/README.md), [execution](exec/README.md), [store](store/README.md), [UI hub](uihub/README.md). Supporting packages own config, credentials, clocks, health, quota, sessions, synthetic data, and venue lifecycle. Test: `go test ./internal/...`.
