// Command genjournal writes a deterministic synthetic trading day into a SQLite
// journal, replayable by `etape -replay <day>`. It is the data source for the
// UI Plan 6 Playwright E2E and for re-capturing a mock-engine fixture — no OpenD,
// no market hours, byte-for-byte reproducible.
package main

import (
	"flag"
	"log"

	"github.com/earlisreal/eTape/engine/internal/demojournal"
)

func main() {
	db := flag.String("db", "", "output SQLite journal path (required)")
	day := flag.String("day", "2026-01-02", "ET trading day to stamp (YYYY-MM-DD)")
	flag.Parse()
	if *db == "" {
		log.Fatal("genjournal: -db is required")
	}
	if err := demojournal.Generate(*db, *day); err != nil {
		log.Fatalf("genjournal: %v", err)
	}
	log.Printf("genjournal: wrote %s for %s", *db, *day)
}
