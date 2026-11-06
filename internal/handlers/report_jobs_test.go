package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tms-backend/internal/handlers"
	"tms-backend/internal/reports"
)

type stubSource struct {
	n, i int
}

func (s *stubSource) Next() (reports.Row, bool, error) {
	if s.i >= s.n {
		return reports.Row{}, false, nil
	}
	s.i++
	v := 27.5
	return reports.Row{IP: "10.0.0.1", ProbeNo: 1, Temp: &v, Time: time.Now()}, true, nil
}
func (s *stubSource) Close() error { return nil }
func (s *stubSource) Total() int64 { return int64(s.n) }

func useStubManager(t *testing.T, rows, queueLen int) {
	t.Helper()
	m := reports.NewManager(t.TempDir(), func(reports.Params) (reports.Source, error) {
		return &stubSource{n: rows}, nil
	}, 0, queueLen, time.Hour)
	m.Start()
	handlers.ReportManager = m
	t.Cleanup(func() { m.Stop(); handlers.ReportManager = nil })
}

func postJob(t *testing.T, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/reports/templog/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return mustTest(t, newTestApp(), req)
}

func TestReportJob_Unavailable(t *testing.T) {
	handlers.ReportManager = nil
	if resp := postJob(t, `{"startDate":"2026-11-05","endDate":"2026-11-06"}`); resp.StatusCode != 503 {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestReportJob_Validation(t *testing.T) {
	useStubManager(t, 1, 2)
	for _, body := range []string{`{}`, `{"startDate":"2026-11-05"}`, `{"startDate":"05/11/2026","endDate":"2026-11-06"}`, `{"startDate":"2026-11-05","endDate":"nope"}`} {
		if resp := postJob(t, body); resp.StatusCode != 400 {
			t.Errorf("body %s: status = %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestReportJob_EndToEnd(t *testing.T) {
	useStubManager(t, 120, 2)
	app := newTestApp()

	resp := postJob(t, `{"startDate":"2026-11-05","endDate":"2026-11-06","devices":"10.0.0.1"}`)
	if resp.StatusCode != 202 {
		t.Fatalf("create status = %d, want 202", resp.StatusCode)
	}
	var job struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal([]byte(bodyString(t, resp.Body)), &job); err != nil || job.JobID == "" {
		t.Fatalf("bad create response: %v", err)
	}

	// file before ready (or unknown) must not be served as a PDF
	var status string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := mustTest(t, app, httptest.NewRequest("GET", "/api/reports/jobs/"+job.JobID, nil))
		var j struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal([]byte(bodyString(t, r.Body)), &j)
		status = j.Status
		if status == "done" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "done" {
		t.Fatalf("job status = %q, want done", status)
	}

	fr := mustTest(t, app, httptest.NewRequest("GET", "/api/reports/jobs/"+job.JobID+"/file", nil))
	if fr.StatusCode != 200 || fr.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("file: status=%d type=%q", fr.StatusCode, fr.Header.Get("Content-Type"))
	}
	pdfBody := bodyString(t, fr.Body)
	fr.Body.Close()
	if !strings.HasPrefix(pdfBody, "%PDF-") {
		t.Error("file is not a PDF")
	}
}

func TestReportJob_UnknownAndNotReady(t *testing.T) {
	useStubManager(t, 1, 2)
	app := newTestApp()
	for _, path := range []string{"/api/reports/jobs/nope", "/api/reports/jobs/nope/file"} {
		if r := mustTest(t, app, httptest.NewRequest("GET", path, nil)); r.StatusCode != 404 {
			t.Errorf("%s: status = %d, want 404", path, r.StatusCode)
		}
	}
}

func TestReportJob_QueueFull429(t *testing.T) {
	gate := make(chan struct{})
	m := reports.NewManager(t.TempDir(), func(reports.Params) (reports.Source, error) {
		<-gate // hold the worker
		return &stubSource{n: 1}, nil
	}, 0, 1, time.Hour)
	m.Start()
	handlers.ReportManager = m
	t.Cleanup(func() { close(gate); m.Stop(); handlers.ReportManager = nil })

	body := `{"startDate":"2026-11-05","endDate":"2026-11-06"}`
	postJob(t, body) // taken by worker (blocked in open)
	time.Sleep(50 * time.Millisecond)
	if r := postJob(t, body); r.StatusCode != 202 { // fills the queue
		t.Fatalf("second status = %d, want 202", r.StatusCode)
	}
	if r := postJob(t, body); r.StatusCode != 429 {
		t.Fatalf("third status = %d, want 429", r.StatusCode)
	}
}
