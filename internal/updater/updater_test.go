package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	for in, ok := range map[string]bool{
		"2.1.0": true, "v2.1.0": true, "10.0.12": true,
		"dev": false, "2.1": false, "2.1.0-dirty": false, "2.1.0-3-gabc123": false,
		"2.1.0-rc1": false, "02.1.0": false, "2.1.x": false, "": false,
	} {
		if _, got := parseVersion(in); got != ok {
			t.Errorf("parseVersion(%q) ok = %v, want %v", in, got, ok)
		}
	}
	a, _ := parseVersion("2.9.0")
	b, _ := parseVersion("2.10.0")
	if !a.less(b) || b.less(a) || a.less(a) {
		t.Errorf("less is wrong for 2.9.0 vs 2.10.0")
	}
}

func TestWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	any, _ := ParseWindow("")
	if !any.Contains(at(13, 0)) {
		t.Error("empty window should allow any time")
	}
	w, err := ParseWindow("02:00-04:00")
	if err != nil {
		t.Fatal(err)
	}
	if !w.Contains(at(2, 0)) || !w.Contains(at(3, 59)) || w.Contains(at(4, 0)) || w.Contains(at(1, 59)) {
		t.Error("02:00-04:00 boundaries wrong")
	}
	night, _ := ParseWindow("23:00-01:00")
	if !night.Contains(at(23, 30)) || !night.Contains(at(0, 30)) || night.Contains(at(12, 0)) {
		t.Error("window across midnight wrong")
	}
	for _, bad := range []string{"02:00", "2-4", "25:00-01:00", "02:00-02:00"} {
		if _, err := ParseWindow(bad); err == nil {
			t.Errorf("ParseWindow(%q) should fail", bad)
		}
	}
}

func TestParseChecksum(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	in := "1111111111111111111111111111111111111111111111111111111111111111  other.exe\n" + sum + " *tms-backend.exe\n"
	got, err := parseChecksum(strings.NewReader(in), AssetName)
	if err != nil || got != sum {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := parseChecksum(strings.NewReader("x  y\n"), AssetName); err == nil {
		t.Fatal("want error for missing entry")
	}
}

// fakeGitHub serves a latest release with the given tag and exe bytes.
type fakeGitHub struct {
	*httptest.Server
	tag      string
	exe      []byte
	checksum string // overrides the real sha256 when set
}

func newFakeGitHub(t *testing.T, tag string, exe []byte) *fakeGitHub {
	f := &fakeGitHub{tag: tag, exe: exe}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			json.NewEncoder(w).Encode(map[string]any{
				"tag_name": f.tag,
				"assets": []map[string]string{
					{"name": AssetName, "browser_download_url": f.URL + "/exe"},
					{"name": ChecksumsName, "browser_download_url": f.URL + "/sums"},
				},
			})
		case "/exe":
			w.Write(f.exe)
		case "/sums":
			sum := f.checksum
			if sum == "" {
				h := sha256.Sum256(f.exe)
				sum = hex.EncodeToString(h[:])
			}
			w.Write([]byte(sum + "  " + AssetName + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func newTestService(t *testing.T, gh *fakeGitHub, current string) (*Service, *atomic.Bool) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "tms-backend.exe")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	restarted := &atomic.Bool{}
	s := New(Config{
		Enabled:  true,
		APIURL:   gh.URL + "/latest",
		Interval: time.Hour,
		ExePath:  exe,
		Restart:  func() { restarted.Store(true) },
	}, current)
	return s, restarted
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCycleAppliesUpdate(t *testing.T) {
	gh := newFakeGitHub(t, "v2.1.0", []byte("new binary"))
	s, _ := newTestService(t, gh, "2.0.7")

	if !s.cycle() {
		t.Fatalf("cycle did not apply; status %+v", s.Status())
	}
	if !s.RestartPending() {
		t.Error("restart not pending")
	}
	if got := readFile(t, s.files.exe); got != "new binary" {
		t.Errorf("exe = %q", got)
	}
	if got := readFile(t, s.files.oldExe); got != "old binary" {
		t.Errorf("exe.old = %q", got)
	}
	st := loadState(s.files.state)
	if !st.Pending || st.From != "2.0.7" || st.To != "2.1.0" {
		t.Errorf("state = %+v", st)
	}
}

func TestCycleRejectsBadChecksum(t *testing.T) {
	gh := newFakeGitHub(t, "v2.1.0", []byte("new binary"))
	gh.checksum = strings.Repeat("0", 64)
	s, restarted := newTestService(t, gh, "2.0.7")

	if s.cycle() || restarted.Load() {
		t.Fatal("update applied despite checksum mismatch")
	}
	if st := s.Status(); st.State != StateError || !strings.Contains(st.LastError, "checksum") {
		t.Errorf("status = %+v", st)
	}
	if got := readFile(t, s.files.exe); got != "old binary" {
		t.Errorf("exe = %q", got)
	}
	if _, err := os.Stat(s.files.newExe); !os.IsNotExist(err) {
		t.Error("exe.new left behind")
	}
}

func TestCycleUpToDateAndSkipped(t *testing.T) {
	gh := newFakeGitHub(t, "v2.1.0", []byte("new binary"))
	s, _ := newTestService(t, gh, "2.1.0")
	if s.cycle() || s.Status().State != StateUpToDate {
		t.Fatalf("same version: status %+v", s.Status())
	}

	s, restarted := newTestService(t, gh, "2.0.7")
	saveState(s.files.state, state{Skip: []string{"2.1.0"}})
	if s.cycle() || restarted.Load() {
		t.Fatal("skipped version was applied")
	}
	if st := s.Status(); st.State != StateUpToDate || !strings.Contains(st.Reason, "rolled back") {
		t.Errorf("status = %+v", st)
	}
}

func TestCycleWaitsWhileBusy(t *testing.T) {
	gh := newFakeGitHub(t, "v2.1.0", []byte("new binary"))
	s, restarted := newTestService(t, gh, "2.0.7")
	var busy atomic.Bool
	busy.Store(true)
	s.cfg.Busy = busy.Load

	done := make(chan bool)
	go func() { done <- s.cycle() }()

	deadline := time.Now().Add(5 * time.Second)
	for s.Status().State != StateWaiting {
		if time.Now().After(deadline) {
			t.Fatalf("never reached waiting; status %+v", s.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.RestartPending() || readFile(t, s.files.exe) != "old binary" {
		t.Fatal("applied while busy")
	}
	busy.Store(false)
	s.CheckNow()
	if !<-done || !s.RestartPending() {
		t.Fatal("update not applied once idle")
	}
	_ = restarted
}

func TestLoopRestartsAfterUpdate(t *testing.T) {
	gh := newFakeGitHub(t, "v2.1.0", []byte("new binary"))
	s, restarted := newTestService(t, gh, "2.0.7")
	s.Start()
	defer s.Stop()
	s.CheckNow()

	deadline := time.Now().Add(5 * time.Second)
	for !restarted.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("Restart never called; status %+v", s.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.Stop() // must not hang after the loop has exited
}

func TestDevBuildIsDisabled(t *testing.T) {
	gh := newFakeGitHub(t, "v2.1.0", nil)
	s, _ := newTestService(t, gh, "2.0.7-4-gabc-dirty")
	if s.Status().State != StateDisabled {
		t.Errorf("status = %+v", s.Status())
	}
}

func TestHousekeepRespectsPending(t *testing.T) {
	gh := newFakeGitHub(t, "v2.1.0", nil)
	s, _ := newTestService(t, gh, "2.0.7")
	os.WriteFile(s.files.oldExe, []byte("x"), 0o644)
	os.WriteFile(s.files.badExe, []byte("x"), 0o644)

	saveState(s.files.state, state{Pending: true, To: "2.0.7"})
	s.housekeep()
	if _, err := os.Stat(s.files.oldExe); err != nil {
		t.Error("exe.old removed while update pending")
	}

	saveState(s.files.state, state{})
	s.housekeep()
	for _, p := range []string{s.files.oldExe, s.files.badExe} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s not cleaned up", p)
		}
	}
}

func setupSwapped(t *testing.T) (files, state) {
	dir := t.TempDir()
	f := filesFor(filepath.Join(dir, "tms-backend.exe"))
	os.WriteFile(f.exe, []byte("new binary"), 0o755)
	os.WriteFile(f.oldExe, []byte("old binary"), 0o755)
	st := state{Pending: true, From: "2.0.7", To: "2.1.0"}
	saveState(f.state, st)
	return f, st
}

func TestWatchHealthy(t *testing.T) {
	f, st := setupSwapped(t)
	start := func(exe string) (*os.Process, <-chan struct{}, error) {
		os.WriteFile(f.marker, []byte("2.1.0"), 0o644) // what MarkHealthy does
		return nil, make(chan struct{}), nil
	}
	if err := watch(f, st, start, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := loadState(f.state); got.Pending || len(got.Skip) != 0 {
		t.Errorf("state = %+v", got)
	}
	if got := readFile(t, f.exe); got != "new binary" {
		t.Errorf("exe = %q", got)
	}
}

func TestWatchRollsBackWhenNewExeExits(t *testing.T) {
	f, st := setupSwapped(t)
	var starts []string
	start := func(exe string) (*os.Process, <-chan struct{}, error) {
		starts = append(starts, readFile(t, exe))
		exited := make(chan struct{})
		close(exited) // crashes immediately
		return nil, exited, nil
	}
	if err := watch(f, st, start, 5*time.Second); err == nil {
		t.Fatal("want rollback error")
	}
	if got := readFile(t, f.exe); got != "old binary" {
		t.Errorf("exe = %q, want old binary restored", got)
	}
	if got := readFile(t, f.badExe); got != "new binary" {
		t.Errorf("exe.bad = %q", got)
	}
	if got := loadState(f.state); got.Pending || !got.skips("2.1.0") {
		t.Errorf("state = %+v", got)
	}
	if len(starts) != 2 || starts[1] != "old binary" {
		t.Errorf("starts = %q, want new then old", starts)
	}
}

func TestWatchRollsBackOnTimeout(t *testing.T) {
	f, st := setupSwapped(t)
	start := func(string) (*os.Process, <-chan struct{}, error) { return nil, make(chan struct{}), nil }
	if err := watch(f, st, start, 600*time.Millisecond); err == nil {
		t.Fatal("want rollback error")
	}
	if got := readFile(t, f.exe); got != "old binary" {
		t.Errorf("exe = %q", got)
	}
}
