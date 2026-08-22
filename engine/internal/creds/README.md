# Credentials

Loads/saves venue secrets in the selected runtime profile's `credentials.json`
through atomic writes. The real `~/.eTape/credentials.json` path is an explicit
user/migration opt-in. Never logs secret values or commits runtime files. Test:
`go test ./internal/creds`.
