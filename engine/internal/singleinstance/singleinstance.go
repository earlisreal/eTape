// Package singleinstance guards against two eTape engine processes sharing
// one SQLite store, moomoo OpenD connection, and uihub port. Both are
// keyed on the store's DB path, so a second launch pointed at the same
// config collides on all three at once (journal seq-number PK collisions,
// double-spent OpenD subscription quota, and a silent EADDRINUSE that used
// to leave a headless zombie process behind). Acquire blocks that second
// launch before any of those resources are touched.
package singleinstance

import "errors"

// ErrAlreadyRunning means another live process already holds the lock at
// the given path. Callers should treat this as "an instance is already up"
// rather than an error worth logging loudly.
var ErrAlreadyRunning = errors.New("singleinstance: another instance is already running")

// Acquire takes an exclusive, non-blocking advisory lock on path, creating
// the file if it doesn't exist. It returns ErrAlreadyRunning if another live
// process already holds it.
//
// The lock is advisory and OS-held: it is released automatically when the
// holding process exits or crashes (unlike a plain lock file guarded by
// existence, which would need manual staleness detection). Callers should
// still call the returned release func on a clean shutdown, but a missed
// call on crash is not a stale-lock hazard.
func Acquire(path string) (release func() error, err error) {
	return acquire(path)
}
