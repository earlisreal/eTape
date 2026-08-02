# Credentials

Loads/saves venue secrets in `~/.eTape/credentials.json` through atomic writes. Never logs secret values or commits runtime files. Test: `go test ./internal/creds`.
