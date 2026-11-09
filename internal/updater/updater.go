// Package updater keeps the running tms-backend.exe up to date from GitHub
// Releases: it checks the latest release on an interval, downloads and
// verifies the exe, swaps it in at a safe moment, and restarts through a
// watchdog that rolls back if the new version fails to start.
package updater

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultRepo     = "efangly/tms-client-backend"
	defaultInterval = 6 * time.Hour
	initialDelay    = 2 * time.Minute
	waitPoll        = time.Minute
)

// Config controls the Service. ConfigFromEnv builds it from .env.
type Config struct {
	Enabled  bool
	APIURL   string        // latest-release endpoint
	Interval time.Duration // time between checks
	Window   Window        // when an update may be applied
	ExePath  string        // the running binary

	// Busy reports work that must not be cut off by a restart (e.g. a
	// running Report Job). Optional.
	Busy func() bool
	// Restart asks the app to shut down gracefully; the app then calls
	// SpawnWatchdog from its cleanup. Required for applying updates.
	Restart func()
	// OnStatus is called whenever Status changes. Optional.
	OnStatus func(Status)
}

// ConfigFromEnv reads UPDATE_ENABLED, UPDATE_REPO, UPDATE_API_URL,
// UPDATE_CHECK_INTERVAL and UPDATE_WINDOW.
func ConfigFromEnv() (Config, error) {
	cfg := Config{Enabled: true, Interval: defaultInterval}
	if v := os.Getenv("UPDATE_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return cfg, fmt.Errorf("UPDATE_ENABLED: %w", err)
		}
		cfg.Enabled = b
	}
	repo := os.Getenv("UPDATE_REPO")
	if repo == "" {
		repo = defaultRepo
	}
	cfg.APIURL = os.Getenv("UPDATE_API_URL")
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.github.com/repos/" + repo + "/releases/latest"
	}
	if v := os.Getenv("UPDATE_CHECK_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute {
			return cfg, fmt.Errorf("UPDATE_CHECK_INTERVAL %q: must be a duration of at least 1m", v)
		}
		cfg.Interval = d
	}
	w, err := ParseWindow(os.Getenv("UPDATE_WINDOW"))
	if err != nil {
		return cfg, fmt.Errorf("UPDATE_WINDOW: %w", err)
	}
	cfg.Window = w
	exe, err := os.Executable()
	if err != nil {
		return cfg, err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return cfg, err
	}
	cfg.ExePath = exe
	return cfg, nil
}

// State is where the Service is in its cycle.
type State string

const (
	StateDisabled    State = "disabled"
	StateIdle        State = "idle"
	StateChecking    State = "checking"
	StateUpToDate    State = "up-to-date"
	StateDownloading State = "downloading"
	StateWaiting     State = "waiting" // downloaded, waiting for the window / idle app
	StateApplying    State = "applying"
	StateError       State = "error"
)

// Status is a snapshot served on /api/update/status.
type Status struct {
	Current   string     `json:"current"`
	Latest    string     `json:"latest,omitempty"`
	State     State      `json:"state"`
	Reason    string     `json:"reason,omitempty"`
	LastCheck *time.Time `json:"lastCheck,omitempty"`
	LastError string     `json:"lastError,omitempty"`
}

// Service runs the update cycle.
type Service struct {
	cfg     Config
	current string
	files   files
	client  *http.Client
	now     func() time.Time

	checkNow chan struct{}
	stop     chan struct{}
	wg       sync.WaitGroup

	mu             sync.Mutex
	status         Status
	restartPending bool
}

// New creates a Service for the given running version.
func New(cfg Config, current string) *Service {
	s := &Service{
		cfg:      cfg,
		current:  current,
		files:    filesFor(cfg.ExePath),
		client:   &http.Client{Timeout: 10 * time.Minute},
		now:      time.Now,
		checkNow: make(chan struct{}, 1),
		stop:     make(chan struct{}),
		status:   Status{Current: current, State: StateIdle},
	}
	switch {
	case !cfg.Enabled:
		s.status.State, s.status.Reason = StateDisabled, "UPDATE_ENABLED=false"
	case cfg.ExePath == "":
		s.status.State, s.status.Reason = StateDisabled, "exe path unknown"
	default:
		if _, ok := parseVersion(current); !ok {
			s.status.State, s.status.Reason = StateDisabled, "not a release build ("+current+")"
		}
	}
	return s
}

// Global is the app-wide Service, set by main. May be nil.
var Global *Service

// Start cleans up leftovers from earlier updates and starts the check loop.
func (s *Service) Start() {
	if s == nil {
		return
	}
	s.housekeep()
	s.mu.Lock()
	disabled := s.status.State == StateDisabled
	s.mu.Unlock()
	if disabled {
		log.Printf("Updater disabled: %s", s.Status().Reason)
		return
	}
	s.wg.Add(1)
	go s.loop()
}

// Stop ends the check loop, abandoning a waiting update.
func (s *Service) Stop() {
	if s == nil {
		return
	}
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	s.wg.Wait()
}

// CheckNow triggers a check without waiting for the interval.
func (s *Service) CheckNow() {
	select {
	case s.checkNow <- struct{}{}:
	default:
	}
}

// Status returns a snapshot.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// RestartPending reports that an update was swapped in and the app should
// call SpawnWatchdog after shutting down.
func (s *Service) RestartPending() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restartPending
}

func (s *Service) setStatus(f func(*Status)) {
	s.mu.Lock()
	f(&s.status)
	st := s.status
	s.mu.Unlock()
	if s.cfg.OnStatus != nil {
		s.cfg.OnStatus(st)
	}
}

func (s *Service) fail(err error) {
	log.Printf("Updater: %v", err)
	s.setStatus(func(st *Status) { st.State, st.LastError = StateError, err.Error() })
}

// housekeep removes files left by finished updates. While an update is
// pending (the watchdog is verifying it), everything is left alone.
func (s *Service) housekeep() {
	if s.cfg.ExePath == "" || loadState(s.files.state).Pending {
		return
	}
	for _, p := range []string{s.files.newExe, s.files.newExe + ".part", s.files.badExe, s.files.oldExe, s.files.marker} {
		os.Remove(p)
	}
}

func (s *Service) loop() {
	defer s.wg.Done()
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-timer.C:
		case <-s.checkNow:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		if s.cycle() {
			// Update installed. Restart runs the app's cleanup, which calls
			// Stop and waits for this loop, so it must not run on it.
			go s.cfg.Restart()
			return
		}
		timer.Reset(s.cfg.Interval)
	}
}

// cycle runs one check → download → wait → apply pass. It returns true once
// an update has been swapped in and the app must restart.
func (s *Service) cycle() bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	s.setStatus(func(st *Status) { st.State = StateChecking })
	rel, err := s.fetchLatest(ctx)
	now := s.now()
	s.setStatus(func(st *Status) { st.LastCheck = &now })
	if err != nil {
		s.fail(fmt.Errorf("check: %w", err))
		return false
	}
	latest, ok := parseVersion(rel.Version)
	if !ok {
		s.fail(fmt.Errorf("latest release tag %q is not X.Y.Z", rel.Version))
		return false
	}
	cur, _ := parseVersion(s.current)
	s.setStatus(func(st *Status) { st.Latest = latest.String() })
	if !cur.less(latest) {
		s.setStatus(func(st *Status) { st.State, st.Reason, st.LastError = StateUpToDate, "", "" })
		return false
	}
	if loadState(s.files.state).skips(latest.String()) {
		s.setStatus(func(st *Status) {
			st.State, st.Reason = StateUpToDate, "version "+latest.String()+" was rolled back; waiting for a newer release"
		})
		return false
	}

	log.Printf("Updater: downloading %s (current %s)", latest, s.current)
	s.setStatus(func(st *Status) { st.State, st.Reason = StateDownloading, "" })
	if err := s.download(ctx, rel, s.files.newExe); err != nil {
		s.fail(fmt.Errorf("download %s: %w", latest, err))
		return false
	}

	for !s.canApply() {
		s.setStatus(func(st *Status) { st.State, st.Reason = StateWaiting, s.waitReason() })
		select {
		case <-s.stop:
			return false
		case <-s.checkNow: // an explicit check re-evaluates right away
		case <-time.After(waitPoll):
		}
	}

	return s.apply(latest.String())
}

func (s *Service) canApply() bool {
	return s.cfg.Window.Contains(s.now()) && (s.cfg.Busy == nil || !s.cfg.Busy())
}

func (s *Service) waitReason() string {
	if !s.cfg.Window.Contains(s.now()) {
		return "waiting for update window " + s.cfg.Window.String()
	}
	return "waiting for running jobs to finish"
}

func (s *Service) apply(to string) bool {
	if s.cfg.Restart == nil {
		s.fail(errors.New("no restart hook configured"))
		return false
	}
	s.setStatus(func(st *Status) { st.State, st.Reason = StateApplying, "restarting into "+to })
	st := loadState(s.files.state)
	os.Remove(s.files.marker)
	if err := swap(s.files); err != nil {
		s.fail(err)
		return false
	}
	st.Pending, st.From, st.To, st.StartedAt = true, s.current, to, s.now()
	if err := saveState(s.files.state, st); err != nil {
		// Without the state the watchdog can't verify; undo the swap.
		if rerr := os.Rename(s.files.exe, s.files.newExe); rerr == nil {
			os.Rename(s.files.oldExe, s.files.exe)
		}
		s.fail(fmt.Errorf("save update state: %w", err))
		return false
	}
	log.Printf("Updater: installed %s, restarting", to)
	s.mu.Lock()
	s.restartPending = true
	s.mu.Unlock()
	return true
}

// Window is a daily HH:MM-HH:MM range (may cross midnight). The zero
// Window allows any time.
type Window struct {
	from, to int // minutes after midnight
	set      bool
}

// ParseWindow parses "02:00-04:00"; "" means any time.
func ParseWindow(s string) (Window, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Window{}, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return Window{}, fmt.Errorf("%q: want HH:MM-HH:MM", s)
	}
	from, err1 := parseClock(a)
	to, err2 := parseClock(b)
	if err1 != nil || err2 != nil || from == to {
		return Window{}, fmt.Errorf("%q: want HH:MM-HH:MM", s)
	}
	return Window{from: from, to: to, set: true}, nil
}

func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Contains reports whether t (local time) is inside the window.
func (w Window) Contains(t time.Time) bool {
	if !w.set {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if w.from < w.to {
		return m >= w.from && m < w.to
	}
	return m >= w.from || m < w.to
}

func (w Window) String() string {
	if !w.set {
		return "any time"
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.from/60, w.from%60, w.to/60, w.to%60)
}
