package tickstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/earlisreal/eTape/engine/internal/atomicfile"
)

type runState struct {
	Owner   string
	Run     string
	Running bool
}

var runIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// A durable run marker covers the small interval between sealed segments too.
func (s *Store) recoverRun() error {
	path := filepath.Join(s.opt.Directory, "recorder-state.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return errors.New("tickstore: run marker is not a regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var prior runState
	if err := json.Unmarshal(body, &prior); err != nil {
		return err
	}
	if prior.Owner != "etape.tickstore" || !runIDPattern.MatchString(prior.Run) {
		return errors.New("tickstore: unrecognized run marker; preserve and inspect it")
	}
	if prior.Running {
		s.incomplete = true
		s.recovered = append(s.recovered, prior.Run)
	}
	return nil
}

func (s *Store) writeRun(running bool) error {
	if err := s.ensureBudget(8 << 10); err != nil {
		return err
	}
	body, err := json.Marshal(runState{"etape.tickstore", s.run, running})
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(s.opt.Directory, "recorder-state.json"), body, 0600)
}
