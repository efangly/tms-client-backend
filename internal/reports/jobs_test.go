package reports

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeSource struct {
	rows    []Row
	i       int
	err     error
	gate    chan struct{} // if set, Next blocks until closed
	onClose func()
}

func (f *fakeSource) Next() (Row, bool, error) {
	if f.gate != nil {
		<-f.gate
	}
	if f.err != nil {
		return Row{}, false, f.err
	}
	if f.i >= len(f.rows) {
		return Row{}, false, nil
	}
	r := f.rows[f.i]
	f.i++
	return r, true, nil
}
func (f *fakeSource) Close() error {
	if f.onClose != nil {
		f.onClose()
	}
	return nil
}
func (f *fakeSource) Total() int64 { return int64(len(f.rows)) }

func waitFor(t *testing.T, m *Manager, id string, want Status) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := m.Get(id)
		if ok && j.Status == want {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := m.Get(id)
	t.Fatalf("job %s never reached %s (now %+v)", id, want, j)
	return Job{}
}

func newTestManager(t *testing.T, open OpenFunc, maxRows, queueLen int) *Manager {
	t.Helper()
	m := NewManager(t.TempDir(), open, maxRows, queueLen, time.Hour)
	m.Start()
	t.Cleanup(m.Stop)
	return m
}

func TestManager_JobCompletes(t *testing.T) {
	m := newTestManager(t, func(Params) (Source, error) { return &fakeSource{rows: makeRows(50)}, nil }, 0, 10)
	job, err := m.Submit(Params{StartDate: "2026-11-05", EndDate: "2026-11-06"})
	if err != nil {
		t.Fatal(err)
	}
	if len(job.ID) < 32 {
		t.Errorf("job id %q should be an unguessable uuid", job.ID)
	}
	done := waitFor(t, m, job.ID, StatusDone)
	if done.Progress != 100 {
		t.Errorf("progress = %d", done.Progress)
	}
	path, ok := m.FilePath(job.ID)
	if !ok {
		t.Fatal("no file for done job")
	}
	if st, err := os.Stat(path); err != nil || st.Size() == 0 {
		t.Fatalf("file missing/empty: %v", err)
	}
}

func TestManager_RowLimitFailsJob(t *testing.T) {
	m := newTestManager(t, func(Params) (Source, error) { return &fakeSource{rows: makeRows(20)}, nil }, 10, 10)
	job, _ := m.Submit(Params{StartDate: "2026-11-05", EndDate: "2026-11-06"})
	failed := waitFor(t, m, job.ID, StatusFailed)
	if failed.Error == "" {
		t.Error("failed job should explain why")
	}
	if _, ok := m.FilePath(job.ID); ok {
		t.Error("failed job must have no file")
	}
}

func TestManager_SourceErrorFailsJob(t *testing.T) {
	m := newTestManager(t, func(Params) (Source, error) {
		return &fakeSource{rows: makeRows(5), err: errors.New("db down")}, nil
	}, 0, 10)
	job, _ := m.Submit(Params{})
	waitFor(t, m, job.ID, StatusFailed)
	entries, _ := os.ReadDir(m.dir)
	if len(entries) != 0 {
		t.Errorf("partial files left behind: %v", entries)
	}
}

func TestManager_QueueFullAndOneAtATime(t *testing.T) {
	gate := make(chan struct{})
	var mu sync.Mutex
	running, maxRunning := 0, 0
	open := func(Params) (Source, error) {
		mu.Lock()
		running++
		if running > maxRunning {
			maxRunning = running
		}
		mu.Unlock()
		return &fakeSource{rows: makeRows(1), gate: gate, onClose: func() { mu.Lock(); running--; mu.Unlock() }}, nil
	}
	m := newTestManager(t, open, 0, 2)

	first, _ := m.Submit(Params{})
	waitFor(t, m, first.ID, StatusRunning)
	// queue holds 2 more
	for i := 0; i < 2; i++ {
		if _, err := m.Submit(Params{}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	if _, err := m.Submit(Params{}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull", err)
	}
	close(gate)
	waitFor(t, m, first.ID, StatusDone)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if maxRunning != 1 {
		t.Errorf("max concurrent = %d, want 1", maxRunning)
	}
}

func TestManager_CleanupRemovesExpired(t *testing.T) {
	m := newTestManager(t, func(Params) (Source, error) { return &fakeSource{rows: makeRows(3)}, nil }, 0, 10)
	job, _ := m.Submit(Params{})
	waitFor(t, m, job.ID, StatusDone)
	path, _ := m.FilePath(job.ID)

	m.Cleanup(time.Now().Add(30 * time.Minute))
	if _, ok := m.Get(job.ID); !ok {
		t.Fatal("job removed before ttl")
	}
	m.Cleanup(time.Now().Add(2 * time.Hour))
	if _, ok := m.Get(job.ID); ok {
		t.Error("expired job still known")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expired file still on disk")
	}
}

func TestManager_StartClearsStaleFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "old.pdf")
	_ = os.WriteFile(stale, []byte("x"), 0o644)
	m := NewManager(dir, func(Params) (Source, error) { return &fakeSource{}, nil }, 0, 1, time.Hour)
	m.Start()
	defer m.Stop()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale file from a previous run should be removed")
	}
}

func TestManager_Busy(t *testing.T) {
	gate := make(chan struct{})
	m := newTestManager(t, func(Params) (Source, error) {
		return &fakeSource{rows: makeRows(1), gate: gate}, nil
	}, 0, 10)
	if m.Busy() {
		t.Fatal("idle manager reports busy")
	}
	job, _ := m.Submit(Params{})
	if !m.Busy() {
		t.Fatal("queued job should make the manager busy")
	}
	waitFor(t, m, job.ID, StatusRunning)
	if !m.Busy() {
		t.Fatal("running job should make the manager busy")
	}
	close(gate)
	waitFor(t, m, job.ID, StatusDone)
	if m.Busy() {
		t.Fatal("finished job should not make the manager busy")
	}
}
