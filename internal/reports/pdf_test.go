package reports

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func sliceSource(rows []Row) func() (Row, bool, error) {
	i := 0
	return func() (Row, bool, error) {
		if i >= len(rows) {
			return Row{}, false, nil
		}
		r := rows[i]
		i++
		return r, true, nil
	}
}

func makeRows(n int) []Row {
	t0 := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	v := 28.5
	rows := make([]Row, n)
	for i := range rows {
		rows[i] = Row{IP: "192.168.1.111", ProbeNo: 1, Temp: &v, Time: t0.Add(time.Duration(i) * 5 * time.Minute)}
	}
	return rows
}

func TestWritePDF_ProducesPaginatedPDF(t *testing.T) {
	var buf bytes.Buffer
	res, err := WritePDF(&buf, "2026-11-05", "2026-11-06", sliceSource(makeRows(300)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Fatal("output is not a PDF")
	}
	if res.Rows != 300 {
		t.Errorf("rows = %d, want 300", res.Rows)
	}
	if res.Pages < 2 || res.Pages > 20 {
		t.Errorf("pages = %d, want a few pages for 300 rows", res.Pages)
	}
}

func TestWritePDF_EmptyAndNilTemp(t *testing.T) {
	var buf bytes.Buffer
	res, err := WritePDF(&buf, "2026-11-05", "2026-11-06", sliceSource(nil), 0)
	if err != nil || res.Rows != 0 || res.Pages != 1 {
		t.Fatalf("empty: res=%+v err=%v", res, err)
	}
	buf.Reset()
	rows := []Row{{IP: "10.0.0.1", ProbeNo: 2, Time: time.Now()}}
	if _, err := WritePDF(&buf, "2026-11-05", "2026-11-06", sliceSource(rows), 0); err != nil {
		t.Fatalf("nil temp: %v", err)
	}
}

func TestWritePDF_RowLimit(t *testing.T) {
	var buf bytes.Buffer
	_, err := WritePDF(&buf, "2026-11-05", "2026-11-06", sliceSource(makeRows(11)), 10)
	if !errors.Is(err, ErrTooManyRows) {
		t.Fatalf("err = %v, want ErrTooManyRows", err)
	}
	buf.Reset()
	if _, err := WritePDF(&buf, "2026-11-05", "2026-11-06", sliceSource(makeRows(10)), 10); err != nil {
		t.Fatalf("exactly at limit should pass: %v", err)
	}
}

func TestWritePDF_SourceError(t *testing.T) {
	boom := errors.New("db down")
	var buf bytes.Buffer
	_, err := WritePDF(&buf, "2026-11-05", "2026-11-06", func() (Row, bool, error) { return Row{}, false, boom }, 0)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}
