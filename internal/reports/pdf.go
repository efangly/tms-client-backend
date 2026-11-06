// Package reports builds temperature Report PDFs and runs them as background
// Report Jobs.
package reports

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/go-pdf/fpdf"
)

//go:embed fonts/Sarabun-Regular.ttf
var fontRegular []byte

//go:embed fonts/Sarabun-Bold.ttf
var fontBold []byte

// ErrTooManyRows is returned when a Report would exceed the row limit.
var ErrTooManyRows = errors.New("report exceeds the maximum number of rows")

// Row is one temperature reading in a Report.
type Row struct {
	IP      string
	ProbeNo int
	Temp    *float64
	Time    time.Time
}

// Result describes a finished PDF.
type Result struct {
	Rows  int
	Pages int
}

const (
	footerTitle = "Temperature Monitoring System"
	timeLayout  = "02/01/2006 15:04:05"
	rowHeight   = 6.5
)

// column widths (mm) on A4 portrait with 15mm side margins.
var colWidths = [4]float64{55, 25, 35, 65}

// WritePDF renders the Report table to w, pulling rows from next until it
// reports no more. maxRows <= 0 means no limit. startDate/endDate are
// YYYY-MM-DD and shown in the header as dd/mm/yyyy.
func WritePDF(w io.Writer, startDate, endDate string, next func() (Row, bool, error), maxRows int) (Result, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 15)
	pdf.AddUTF8FontFromBytes("Sarabun", "", fontRegular)
	pdf.AddUTF8FontFromBytes("Sarabun", "B", fontBold)
	pdf.AliasNbPages("{nb}")

	rangeText := fmt.Sprintf("%s - %s", fmtDate(startDate), fmtDate(endDate))

	pdf.SetHeaderFunc(func() {
		pdf.SetFont("Sarabun", "B", 12)
		pdf.CellFormat(0, 7, rangeText, "", 1, "C", false, 0, "")
		pdf.Ln(1)
		pdf.SetFont("Sarabun", "B", 10)
		pdf.SetFillColor(230, 230, 230)
		heads := [4]string{"IP", "Prob No.", "(°C)", "Date / Time"}
		for i, h := range heads {
			pdf.CellFormat(colWidths[i], rowHeight, h, "B", 0, "L", true, 0, "")
		}
		pdf.Ln(-1)
	})
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Sarabun", "", 9)
		pdf.CellFormat(0, 6, footerTitle, "", 0, "L", false, 0, "")
		pdf.SetX(15)
		pdf.CellFormat(0, 6, fmt.Sprintf("%d / {nb}", pdf.PageNo()), "", 0, "R", false, 0, "")
	})

	pdf.AddPage()
	pdf.SetFont("Sarabun", "", 10)

	n := 0
	for {
		row, ok, err := next()
		if err != nil {
			return Result{}, err
		}
		if !ok {
			break
		}
		if maxRows > 0 && n >= maxRows {
			return Result{}, ErrTooManyRows
		}
		temp := "-"
		if row.Temp != nil {
			temp = fmt.Sprintf("%.2f", *row.Temp)
		}
		pdf.CellFormat(colWidths[0], rowHeight, row.IP, "", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[1], rowHeight, fmt.Sprintf("%d", row.ProbeNo), "", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[2], rowHeight, temp, "", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[3], rowHeight, row.Time.Format(timeLayout), "", 1, "L", false, 0, "")
		n++
	}

	pages := pdf.PageNo()
	if err := pdf.Output(w); err != nil {
		return Result{}, err
	}
	return Result{Rows: n, Pages: pages}, nil
}

// fmtDate converts YYYY-MM-DD to dd/mm/yyyy, returning s unchanged if it
// does not parse.
func fmtDate(s string) string {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return s
	}
	return t.Format("02/01/2006")
}
