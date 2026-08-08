# OpenD Feed

Native TCP framing and protobuf client. Handles handshake, serial-number correlation, keepalive, subscriptions, snapshots, K-lines, scanner/news, and trade messages. The reader counts/rate-limits raw push-buffer overflow warnings; established disconnects and successful reconnect resyncs are logged. Default downstream: `127.0.0.1:11111`. `pb/` is generated; never hand-edit. Preserve reconnect resync. Test: `go test ./internal/feed/opend`.
