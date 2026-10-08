# Feeds

`Recorder` is a source-neutral, nonblocking evidence sink. `SourceRef` follows
received reports through cache sorting and MD without changing the WebSocket
contract. Source identity uses recording run, local connection generation,
ingress ordinal and original protobuf list index. Exact provider sequence is
separate from delivery identity. See [archive contract](../tickstore/README.md).

Normalize market streams into ordered engine events. Current live implementation: [OpenD](opend/README.md); the synthetic demo feed enters the same boundaries. Preserve exchange and receive timestamps. Test: `go test ./internal/feed/...`.
