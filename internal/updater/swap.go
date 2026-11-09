package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// files are the paths the updater uses next to the running exe.
//
//	tms-backend.exe      the binary the Run-key launcher starts
//	tms-backend.exe.new  a downloaded, verified update waiting to be applied
//	tms-backend.exe.old  the previous binary, kept until the update is confirmed
//	tms-backend.exe.bad  an update that failed to start (rolled back)
//	update_state.json    pending update + versions to skip
//	update_ok            written by the new binary once it starts serving
type files struct {
	exe, newExe, oldExe, badExe, state, marker string
}

func filesFor(exe string) files {
	dir := filepath.Dir(exe)
	return files{
		exe:    exe,
		newExe: exe + ".new",
		oldExe: exe + ".old",
		badExe: exe + ".bad",
		state:  filepath.Join(dir, "update_state.json"),
		marker: filepath.Join(dir, "update_ok"),
	}
}

// state is persisted in update_state.json across the restart.
type state struct {
	// Pending is true from the exe swap until the watchdog (or the new
	// binary itself) confirms the new version started.
	Pending   bool      `json:"pending"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	StartedAt time.Time `json:"startedAt,omitzero"`
	// Skip lists versions that were rolled back; they are never retried.
	// Publish a higher version to fix them.
	Skip []string `json:"skip,omitempty"`
}

func (st state) skips(v string) bool {
	return slices.Contains(st.Skip, v)
}

func loadState(path string) state {
	var st state
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveState(path string, st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// swap moves the running exe aside and puts the downloaded one in its place.
// Windows allows renaming (not deleting) a running exe, so this works while
// the old binary is still executing. On failure the original is restored.
func swap(f files) error {
	if err := os.Remove(f.oldExe); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale %s: %w", f.oldExe, err)
	}
	if err := os.Rename(f.exe, f.oldExe); err != nil {
		return fmt.Errorf("move current exe aside: %w", err)
	}
	if err := os.Rename(f.newExe, f.exe); err != nil {
		if rerr := os.Rename(f.oldExe, f.exe); rerr != nil {
			return fmt.Errorf("install new exe: %v (restore also failed: %v)", err, rerr)
		}
		return fmt.Errorf("install new exe: %w", err)
	}
	return nil
}

// rollback puts the previous exe back and keeps the failed one as .bad.
func rollback(f files) error {
	os.Remove(f.badExe)
	if err := os.Rename(f.exe, f.badExe); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("move failed exe aside: %w", err)
	}
	if err := os.Rename(f.oldExe, f.exe); err != nil {
		return fmt.Errorf("restore previous exe: %w", err)
	}
	return nil
}
