package handlers

import (
	"testing"
	"time"

	"tms-backend/internal/reports"
)

type sliceStream struct {
	rows []reports.Row
	i    int
}

func (s *sliceStream) peek() (reports.Row, bool, error) {
	if s.i >= len(s.rows) {
		return reports.Row{}, false, nil
	}
	return s.rows[s.i], true, nil
}
func (s *sliceStream) Next() (reports.Row, bool, error) {
	r, ok, err := s.peek()
	if ok {
		s.i++
	}
	return r, ok, err
}
func (s *sliceStream) Close() error { return nil }
func (s *sliceStream) Total() int64 { return int64(len(s.rows)) }

func TestMergedStream_KeepsIPProbeTimeOrder(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 1, 1, h, 0, 0, 0, time.UTC) }
	live := &sliceStream{rows: []reports.Row{
		{IP: "10.0.0.1", ProbeNo: 1, Time: at(10)},
		{IP: "10.0.0.2", ProbeNo: 1, Time: at(10)},
	}}
	arch := &sliceStream{rows: []reports.Row{
		{IP: "10.0.0.1", ProbeNo: 1, Time: at(1)},
		{IP: "10.0.0.1", ProbeNo: 2, Time: at(1)},
		{IP: "10.0.0.2", ProbeNo: 1, Time: at(2)},
	}}
	m := &mergedStream{a: live, b: arch}
	if m.Total() != 5 {
		t.Errorf("total = %d, want 5", m.Total())
	}

	var got []reports.Row
	for {
		r, ok, err := m.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		got = append(got, r)
	}
	want := []struct {
		ip    string
		probe int
		hour  int
	}{{"10.0.0.1", 1, 1}, {"10.0.0.1", 1, 10}, {"10.0.0.1", 2, 1}, {"10.0.0.2", 1, 2}, {"10.0.0.2", 1, 10}}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].IP != w.ip || got[i].ProbeNo != w.probe || got[i].Time.Hour() != w.hour {
			t.Errorf("row %d = %s/%d/%dh, want %s/%d/%dh", i, got[i].IP, got[i].ProbeNo, got[i].Time.Hour(), w.ip, w.probe, w.hour)
		}
	}
}
