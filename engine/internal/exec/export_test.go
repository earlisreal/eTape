package exec

// export_test.go exposes Core's internal state to exec_test (the external test
// package) for whitebox assertions. It is compiled only for `go test` — never
// part of the production binary — and deliberately imports nothing beyond the
// standard library so it cannot introduce the broker/sim <-> exec import cycle
// that forced core_test.go (and core_lifecycle_test.go) into package exec_test.
//
// StateForTest returns Core's live *State. Callers must only read it, and only
// when no other goroutine is concurrently mutating it (e.g. right after Recover,
// before Run starts) to stay race-free.
func (c *Core) StateForTest() *State { return c.state }
