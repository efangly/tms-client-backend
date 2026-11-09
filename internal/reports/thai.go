package reports

import (
	"strings"

	"github.com/go-pdf/fpdf"
)

// The PDF library places Thai combining marks straight from the font's glyph
// table and does no OpenType mark positioning, so a tone mark that follows an
// upper vowel (ั ิ ี ึ ื ็ ํ) lands on top of it: "ตั้ง" and "ชื่อ" come out with
// the marks overlapping. thaiCell works around this by drawing such tone marks
// separately, raised above the vowel.

// liftEm is how far (in font-size units) a tone mark is raised above an
// upper vowel.
const liftEm = 0.23

func isUpperVowel(r rune) bool {
	return r == 0x0E31 || (r >= 0x0E34 && r <= 0x0E37) || r == 0x0E47 || r == 0x0E4D
}

func isToneMark(r rune) bool { return r >= 0x0E48 && r <= 0x0E4B }

// liftedMark is a tone mark that must be drawn above an upper vowel; before is
// the (stripped) text preceding it, whose width gives the mark's x offset.
type liftedMark struct {
	before string
	mark   rune
}

// splitStackedMarks removes tone marks that follow an upper vowel from s and
// returns them separately. Zero-width marks do not change the text's width, so
// layout of the stripped text is identical to the original.
func splitStackedMarks(s string) (string, []liftedMark) {
	var out []rune
	var marks []liftedMark
	var prev rune
	for _, r := range s {
		if isToneMark(r) && isUpperVowel(prev) {
			marks = append(marks, liftedMark{before: string(out), mark: r})
		} else {
			out = append(out, r)
		}
		prev = r
	}
	return string(out), marks
}

// thaiCell is CellFormat that also renders stacked Thai tone marks correctly.
// Text without stacked marks takes the plain CellFormat path.
func thaiCell(pdf *fpdf.Fpdf, w, h float64, txt, border string, ln int, align string, fill bool) {
	stripped, marks := splitStackedMarks(txt)
	x, y := pdf.GetX(), pdf.GetY()
	pdf.CellFormat(w, h, stripped, border, ln, align, fill, 0, "")
	if len(marks) == 0 {
		return
	}

	_, size := pdf.GetFontSize() // size in user units (mm)
	baseline := y + h/2 + 0.3*size
	cellW := w
	if cellW == 0 { // 0 means "to the right margin"
		pageW, _ := pdf.GetPageSize()
		_, _, right, _ := pdf.GetMargins()
		cellW = pageW - right - x
	}
	strW := pdf.GetStringWidth(stripped)
	margin := pdf.GetCellMargin()
	var start float64
	switch {
	case strings.Contains(align, "R"):
		start = x + cellW - margin - strW
	case strings.Contains(align, "C"):
		start = x + (cellW-strW)/2
	default:
		start = x + margin
	}
	for _, m := range marks {
		pdf.Text(start+pdf.GetStringWidth(m.before), baseline-liftEm*size, string(m.mark))
	}
}
