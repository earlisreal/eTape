# OpenD Feed

Native TCP framing and protobuf client. Handles handshake, serial-number correlation, keepalive, subscriptions, snapshots, K-lines, scanner/news, and trade messages. Default downstream: `127.0.0.1:11111`. `pb/` is generated; never hand-edit. Preserve reconnect resync. Test: `go test ./internal/feed/opend`.
