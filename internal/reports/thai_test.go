package reports

import "testing"

func TestSplitStackedMarks(t *testing.T) {
	cases := []struct {
		in       string
		stripped string
		prefixes []string // text before each lifted mark
		marks    []rune
	}{
		{"ตั้งแต่", "ตังแต่", []string{"ตั"}, []rune{'้'}},
		{"ชื่อเครื่อง", "ชือเครือง", []string{"ชื", "ชือเครื"}, []rune{'่', '่'}},
		{"ค่า", "ค่า", nil, nil},                   // tone mark directly on a consonant stays
		{"ที่", "ที", []string{"ที"}, []rune{'่'}}, // ี + ่
		{"Prob No.", "Prob No.", nil, nil},
		{"", "", nil, nil},
	}
	for _, c := range cases {
		stripped, parts := splitStackedMarks(c.in)
		if stripped != c.stripped {
			t.Errorf("%q: stripped = %q, want %q", c.in, stripped, c.stripped)
		}
		if len(parts) != len(c.marks) {
			t.Errorf("%q: %d lifted marks, want %d", c.in, len(parts), len(c.marks))
			continue
		}
		for i, p := range parts {
			if p.mark != c.marks[i] || p.before != c.prefixes[i] {
				t.Errorf("%q: mark %d = (%q after %q), want (%q after %q)", c.in, i, p.mark, p.before, c.marks[i], c.prefixes[i])
			}
		}
	}
}
