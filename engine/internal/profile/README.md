# Runtime profiles

`profile.Resolve` owns the config, credentials, SQLite, and default log paths
for one engine run. Development, test, prototype, replay, demo, server, and
migration profiles are isolated by default. A path equal to or below the real
`%USERPROFILE%\.eTape` root is rejected unless the caller explicitly passes
`-allow-real-profile`. `Paths.ValidateDataPath` prevents an isolated config
from escaping its root through `[store].db_path`.

Test: `go test ./internal/profile`.
