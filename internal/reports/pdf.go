// Package reports builds temperature Report PDFs and runs them as background
// Report Jobs.
package reports

import (
	"bytes"
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
	IP          string
	ProbeNo     int
	MachineName string
	Temp        *float64
	Time        time.Time
}

// Options tunes how a Report looks.
type Options struct {
	// Logo is a PNG or JPEG drawn in the page-1 header. Nil or invalid means
	// the Report is generated without a logo.
	Logo []byte
}

// Result describes a finished PDF.
type Result struct {
	Rows  int
	Pages int
}

const (
	reportTitle = "รายงานการบันทึกค่าอุณหภูมิ"
	footerTitle = "Temperature Monitoring System"
	emptyText   = "ไม่พบข้อมูลในช่วงเวลาที่เลือก"
	timeLayout  = "02/01/2006 15:04:05"

	rowHeight  = 6.5
	pageW      = 210.0
	margin     = 15.0
	logoSize   = 21.0 // 60pt, as in the original frontend report
	logoGap    = 3.5
	usableW    = pageW - 2*margin
	fontFamily = "Sarabun"
)

// column widths (mm): IP 20%, Prob No. 10%, name 25%, temp 15%, time 30%.
var colWidths = [5]float64{usableW * 0.20, usableW * 0.10, usableW * 0.25, usableW * 0.15, usableW * 0.30}
var colAlign = [5]string{"L", "C", "L", "C", "C"}
var colHeads = [5]string{"IP", "Prob No.", "ชื่อเครื่อง", "อุณหภูมิ (°C)", "เวลา"}

// WritePDF renders the Report to w, pulling rows from next until it reports
// no more. maxRows <= 0 means no limit. startDate/endDate are YYYY-MM-DD and
// shown in the header as dd/mm/yyyy.
func WritePDF(w io.Writer, startDate, endDate string, next func() (Row, bool, error), maxRows int, opts Options) (Result, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(margin, margin, margin)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddUTF8FontFromBytes(fontFamily, "", fontRegular)
	pdf.AddUTF8FontFromBytes(fontFamily, "B", fontBold)
	pdf.AliasNbPages("{nb}")

	rangeText := fmt.Sprintf("ตั้งแต่วันที่ %s - %s", fmtDate(startDate), fmtDate(endDate))

	logoName := ""
	if len(opts.Logo) > 0 {
		if format, ok := decodeLogo(opts.Logo); ok {
			logoName = "logo"
			pdf.RegisterImageOptionsReader(logoName, fpdf.ImageOptions{ImageType: format}, bytes.NewReader(opts.Logo))
		} else {
			warnBadLogo()
		}
	}

	pdf.SetHeaderFunc(func() {
		if pdf.PageNo() == 1 {
			drawTitleBlock(pdf, logoName, rangeText)
		}
		drawTableHead(pdf)
	})
	pdf.SetFooterFunc(func() {
		pdf.SetY(-13)
		pdf.SetFont(fontFamily, "", 9)
		pdf.SetTextColor(102, 102, 102)
		thaiCell(pdf, 0, 6, footerTitle, "", 0, "L", false)
		pdf.SetX(margin)
		thaiCell(pdf, 0, 6, fmt.Sprintf("หน้า %d / {nb}", pdf.PageNo()), "", 0, "R", false)
		pdf.SetTextColor(0, 0, 0)
	})

	pdf.AddPage()
	pdf.SetFont(fontFamily, "", 9.5)
	pdf.SetDrawColor(204, 204, 204)
	pdf.SetFillColor(250, 250, 250)

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
		name := row.MachineName
		if name == "" {
			name = "-"
		}
		cells := [5]string{row.IP, fmt.Sprintf("%d", row.ProbeNo), name, temp, row.Time.Format(timeLayout)}
		fill := n%2 == 0
		for i, c := range cells {
			end := 0
			if i == len(cells)-1 {
				end = 1
			}
			thaiCell(pdf, colWidths[i], rowHeight, c, "B", end, colAlign[i], fill)
		}
		n++
	}

	if n == 0 {
		pdf.Ln(2)
		thaiCell(pdf, usableW, 12, emptyText, "", 1, "C", false)
	}

	pages := pdf.PageNo()
	if err := pdf.Output(w); err != nil {
		return Result{}, err
	}
	return Result{Rows: n, Pages: pages}, nil
}

// drawTitleBlock draws the page-1 header: logo on the left and the title and
// date range beside it, the whole group centred like the original report.
func drawTitleBlock(pdf *fpdf.Fpdf, logoName, rangeText string) {
	pdf.SetFont(fontFamily, "B", 18)
	titleW := pdf.GetStringWidth(reportTitle)
	pdf.SetFont(fontFamily, "", 12)
	rangeW := pdf.GetStringWidth(rangeText)
	textW := titleW
	if rangeW > textW {
		textW = rangeW
	}

	groupW := textW
	if logoName != "" {
		groupW += logoSize + logoGap
	}
	x := (pageW - groupW) / 2
	y := margin

	if logoName != "" {
		pdf.ImageOptions(logoName, x, y, logoSize, logoSize, false, fpdf.ImageOptions{}, 0, "")
		x += logoSize + logoGap
	}

	// Centre the two text lines against each other, vertically centred on the logo.
	blockH := 9.0 + 6.5
	ty := y + (logoSize-blockH)/2
	pdf.SetFont(fontFamily, "B", 18)
	pdf.SetXY(x+(textW-titleW)/2, ty)
	thaiCell(pdf, titleW, 9, reportTitle, "", 0, "L", false)
	pdf.SetFont(fontFamily, "", 12)
	pdf.SetXY(x+(textW-rangeW)/2, ty+9)
	thaiCell(pdf, rangeW, 6.5, rangeText, "", 0, "L", false)

	pdf.SetXY(margin, y+logoSize+5)
}

// drawTableHead draws the grey column-header row with top and bottom rules.
func drawTableHead(pdf *fpdf.Fpdf) {
	pdf.SetFont(fontFamily, "B", 10)
	pdf.SetFillColor(240, 240, 240)
	pdf.SetDrawColor(0, 0, 0)
	for i, h := range colHeads {
		end := 0
		if i == len(colHeads)-1 {
			end = 1
		}
		thaiCell(pdf, colWidths[i], rowHeight+1, h, "TB", end, colAlign[i], true)
	}
	pdf.SetFont(fontFamily, "", 9.5)
	pdf.SetDrawColor(204, 204, 204)
	pdf.SetFillColor(250, 250, 250)
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
