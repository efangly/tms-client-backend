package reports

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of a Report Job.
type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// ErrQueueFull is returned by Submit when too many jobs are waiting.
var ErrQueueFull = errors.New("report queue is full")

// MaxRows is the default row limit for one Report.
const MaxRows = 100000

// Params selects the data in a Report. Dates are YYYY-MM-DD.
type Params struct {
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Devices   string `json:"devices"`
}

// Job is a snapshot of a Report Job.
type Job struct {
	ID         string     `json:"jobId"`
	Status     Status     `json:"status"`
	Progress   int        `json:"progress"`
	Error      string     `json:"error,omitempty"`
	Rows       int        `json:"rows,omitempty"`
	Pages      int        `json:"pages,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	params   Params
	filePath string
}

// Source streams the rows of a Report in order.
type Source interface {
	Next() (Row, bool, error)
	Close() error
	// Total is the number of rows Next will yield, used for the row limit
	// and progress.
	Total() int64
}

// OpenFunc opens a Source for the given Params.
type OpenFunc func(Params) (Source, error)

// Manager queues Report Jobs and runs them one at a time.
type Manager struct {
	dir     string
	open    OpenFunc
	maxRows int
	ttl     time.Duration

	queue chan string
	stop  chan struct{}
	wg    sync.WaitGroup

	mu   sync.Mutex
	jobs map[string]*Job
}

// NewManager creates a Manager writing files to dir. queueLen is how many
// jobs may wait behind the running one; maxRows <= 0 means MaxRows.
func NewManager(dir string, open OpenFunc, maxRows, queueLen int, ttl time.Duration) *Manager {
	if maxRows <= 0 {
		maxRows = MaxRows
	}
	return &Manager{
		dir: dir, open: open, maxRows: maxRows, ttl: ttl,
		queue: make(chan string, queueLen),
		stop:  make(chan struct{}),
		jobs:  make(map[string]*Job),
	}
}

// Start clears files left by a previous run and launches the worker and
// the expiry sweeper.
func (m *Manager) Start() {
	_ = os.MkdirAll(m.dir, 0o755)
	if entries, err := os.ReadDir(m.dir); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(m.dir, e.Name()))
		}
	}
	m.wg.Add(2)
	go m.worker()
	go m.sweeper()
}

// Stop halts the worker; a running job is allowed to finish.
func (m *Manager) Stop() {
	close(m.stop)
	m.wg.Wait()
}

// Submit enqueues a job, or returns ErrQueueFull.
func (m *Manager) Submit(p Params) (Job, error) {
	j := &Job{ID: uuid.NewString(), Status: StatusQueued, CreatedAt: time.Now(), params: p}
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case m.queue <- j.ID:
		m.jobs[j.ID] = j
		return *j, nil
	default:
		return Job{}, ErrQueueFull
	}
}

// Busy reports whether any job is queued or running.
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.Status == StatusQueued || j.Status == StatusRunning {
			return true
		}
	}
	return false
}

// Get returns a snapshot of the job.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

// FilePath returns the PDF path of a finished job.
func (m *Manager) FilePath(id string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || j.Status != StatusDone {
		return "", false
	}
	return j.filePath, true
}

// Cleanup discards finished jobs older than the ttl as of now, with their files.
func (m *Manager) Cleanup(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, j := range m.jobs {
		if j.FinishedAt == nil || now.Sub(*j.FinishedAt) < m.ttl {
			continue
		}
		if j.filePath != "" {
			_ = os.Remove(j.filePath)
		}
		delete(m.jobs, id)
	}
}

func (m *Manager) update(id string, f func(*Job)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[id]; ok {
		f(j)
	}
}

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			return
		case id := <-m.queue:
			m.run(id)
		}
	}
}

func (m *Manager) sweeper() {
	defer m.wg.Done()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-t.C:
			m.Cleanup(now)
		}
	}
}

func (m *Manager) fail(id string, err error) {
	m.update(id, func(j *Job) {
		now := time.Now()
		j.Status = StatusFailed
		j.Error = err.Error()
		j.FinishedAt = &now
	})
}

func (m *Manager) run(id string) {
	m.update(id, func(j *Job) { j.Status = StatusRunning })
	j, ok := m.Get(id)
	if !ok {
		return
	}

	src, err := m.open(j.params)
	if err != nil {
		m.fail(id, err)
		return
	}
	defer src.Close()

	total := src.Total()
	if total > int64(m.maxRows) {
		m.fail(id, fmt.Errorf("%w: %d rows found, limit is %d — narrow the date range or select fewer devices",
			ErrTooManyRows, total, m.maxRows))
		return
	}

	path := filepath.Join(m.dir, id+".pdf")
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		m.fail(id, err)
		return
	}

	var read int64
	next := func() (Row, bool, error) {
		row, more, err := src.Next()
		if more {
			read++
			if read%1000 == 0 && total > 0 {
				p := int(read * 99 / total)
				m.update(id, func(j *Job) { j.Progress = p })
			}
		}
		return row, more, err
	}

	res, err := WritePDF(f, j.params.StartDate, j.params.EndDate, next, m.maxRows, Options{Logo: LoadLogo()})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		m.fail(id, err)
		return
	}
	m.update(id, func(j *Job) {
		now := time.Now()
		j.Status = StatusDone
		j.Progress = 100
		j.Rows, j.Pages = res.Rows, res.Pages
		j.filePath = path
		j.FinishedAt = &now
	})
}
