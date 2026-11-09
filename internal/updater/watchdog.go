package updater

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WatchdogFlag starts the binary in watchdog mode (no tray, no server):
//
//	tms-backend.exe.old --update-watchdog <path to tms-backend.exe>
const WatchdogFlag = "--update-watchdog"

// watchdogTimeout is how long the new version has to report healthy. Its
// startup waits up to 60s for the network and ~30s for DB retries.
const watchdogTimeout = 3 * time.Minute

// SpawnWatchdog starts the previous binary (now exe.old) in watchdog mode.
// Called by the app's cleanup, after the HTTP server has released its port.
func (s *Service) SpawnWatchdog() error {
	cmd := exec.Command(s.files.oldExe, WatchdogFlag, s.files.exe)
	cmd.Dir = filepath.Dir(s.files.exe)
	detach(cmd)
	return cmd.Start()
}

// RunWatchdog is the entry point for watchdog mode. It starts the new exe,
// waits for it to write the healthy marker, and rolls back otherwise.
func RunWatchdog(exe string) {
	f := filesFor(exe)
	if lf, err := openWatchdogLog(filepath.Dir(exe)); err == nil {
		log.SetOutput(lf)
		defer lf.Close()
	}
	st := loadState(f.state)
	if !st.Pending {
		log.Printf("watchdog: no pending update, nothing to do")
		return
	}
	log.Printf("watchdog: verifying update %s -> %s", st.From, st.To)
	if err := watch(f, st, startExe, watchdogTimeout); err != nil {
		log.Printf("watchdog: %v", err)
	}
}

// startExe launches exe detached; the returned channel closes when it exits.
func startExe(exe string) (*os.Process, <-chan struct{}, error) {
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	return cmd.Process, exited, nil
}

type starter func(exe string) (*os.Process, <-chan struct{}, error)

func watch(f files, st state, start starter, timeout time.Duration) error {
	proc, exited, err := start(f.exe)
	if err == nil {
		err = waitHealthy(f.marker, st.To, exited, timeout)
	} else {
		err = fmt.Errorf("start new exe: %w", err)
	}
	if err == nil {
		// The new binary deletes exe.old once this process has exited.
		st.Pending = false
		log.Printf("watchdog: %s is healthy", st.To)
		return saveState(f.state, st)
	}

	log.Printf("watchdog: %s failed (%v), rolling back to %s", st.To, err, st.From)
	if proc != nil {
		proc.Kill()
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
		}
	}
	if rerr := rollback(f); rerr != nil {
		return fmt.Errorf("rollback failed: %w", rerr)
	}
	st.Pending = false
	st.Skip = append(st.Skip, st.To)
	if serr := saveState(f.state, st); serr != nil {
		log.Printf("watchdog: save state: %v", serr)
	}
	os.Remove(f.marker)
	if _, _, serr := start(f.exe); serr != nil {
		return fmt.Errorf("restart previous exe: %w", serr)
	}
	return fmt.Errorf("rolled back to %s", st.From)
}

func waitHealthy(marker, want string, exited <-chan struct{}, timeout time.Duration) error {
	deadline := time.After(timeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == want {
			return nil
		}
		select {
		case <-exited:
			return errors.New("new exe exited before becoming healthy")
		case <-deadline:
			return fmt.Errorf("new exe not healthy after %s", timeout)
		case <-tick.C:
		}
	}
}

// MarkHealthy is called by the app once it is serving (or has failed for an
// environmental reason such as the database being down — the binary itself
// works). If this run is a pending update, it tells the watchdog, then
// removes exe.old once the watchdog has exited and released it.
func MarkHealthy(current string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return
	}
	f := filesFor(exe)
	st := loadState(f.state)
	if !st.Pending || st.To != current {
		return
	}
	if err := os.WriteFile(f.marker, []byte(current), 0o644); err != nil {
		log.Printf("Updater: write healthy marker: %v", err)
		return
	}
	go func() {
		for range 60 {
			err := os.Remove(f.oldExe)
			if err == nil || errors.Is(err, os.ErrNotExist) {
				// The watchdog is gone. If it never got to clear Pending
				// (e.g. the machine rebooted mid-update), do it here.
				if st := loadState(f.state); st.Pending && st.To == current {
					st.Pending = false
					saveState(f.state, st)
				}
				os.Remove(f.marker)
				log.Printf("Updater: now running %s", current)
				return
			}
			time.Sleep(5 * time.Second)
		}
	}()
}

const maxWatchdogLog = 200 * 1024

func openWatchdogLog(dir string) (*os.File, error) {
	logs := filepath.Join(dir, "logs")
	os.MkdirAll(logs, 0o755)
	p := filepath.Join(logs, "update.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxWatchdogLog {
		if b, err := os.ReadFile(p); err == nil {
			os.WriteFile(p, b[len(b)-maxWatchdogLog/2:], 0o644)
		}
	}
	return os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
