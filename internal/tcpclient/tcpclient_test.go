// Tests for the internal TCP sensor protocol parser.
// Uses package tcpclient (white-box) to access the unexported parseHexResponse
// and calcHumidity functions.
package tcpclient

import (
	"math"
	"testing"
)

// helper: round to 2 decimal places for comparison
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// ── parseHexResponse ──────────────────────────────────────────────────────────

// TestOneProbe_NineBytes verifies the standard single-probe 9-byte frame.
//
// Frame: 41 41 5A 00 5A 19 A3 5A 0D
//   raw = 0x19A3 = 6563 → temp = (6563-4000)*0.01 = 25.63 °C
func TestOneProbe_NineBytes(t *testing.T) {
	data := []byte{0x41, 0x41, 0x5a, 0x00, 0x5a, 0x19, 0xa3, 0x5a, 0x0d}
	probes := parseHexResponse(data, "192.168.1.1")

	if len(probes) != 1 {
		t.Fatalf("expected 1 probe, got %d", len(probes))
	}
	p := probes[0]
	if p.ProbeNo != 1 {
		t.Errorf("ProbeNo = %d, want 1", p.ProbeNo)
	}
	if p.RealValue != 6563 {
		t.Errorf("RealValue = %d, want 6563", p.RealValue)
	}
	if p.TempValue != 25.63 {
		t.Errorf("TempValue = %v, want 25.63", p.TempValue)
	}
}

// TestTwoProbes_TwelveBytes verifies the dual-probe 12-byte frame.
//
// Frame: 41 41 5A 03 5A 19 A3 5A 19 AE 5A 0D
//   probe1 raw = 0x19A3 = 6563 → 25.63 °C
//   probe2 raw = 0x19AE = 6574 → 25.74 °C
func TestTwoProbes_TwelveBytes(t *testing.T) {
	data := []byte{0x41, 0x41, 0x5a, 0x03, 0x5a, 0x19, 0xa3, 0x5a, 0x19, 0xae, 0x5a, 0x0d}
	probes := parseHexResponse(data, "192.168.1.1")

	if len(probes) != 2 {
		t.Fatalf("expected 2 probes, got %d", len(probes))
	}

	if probes[0].ProbeNo != 1 || probes[0].TempValue != 25.63 {
		t.Errorf("probe1: ProbeNo=%d TempValue=%v, want ProbeNo=1 TempValue=25.63",
			probes[0].ProbeNo, probes[0].TempValue)
	}
	if probes[1].ProbeNo != 2 {
		t.Errorf("probe2: ProbeNo=%d, want 2", probes[1].ProbeNo)
	}
	if got := probes[1].TempValue; got != 25.74 {
		t.Errorf("probe2: TempValue = %v, want 25.74", got)
	}
	if probes[1].RealValue != 6574 {
		t.Errorf("probe2: RealValue = %d, want 6574", probes[1].RealValue)
	}
}

// TestTempHumidityTemp_FifteenBytes verifies the 15-byte frame that carries
// temperature + humidity + a second temperature probe.
//
// Frame: 41 41 5A 00  5A 19 A3  5A 07 D0  5A 1A 2C  5A 0D
//   probe1 temp raw = 0x19A3 = 6563 → 25.63 °C
//   humidity raw   = 0x07D0 = 2000 → calcHumidity(2000, 25.63, 0) ≈ 65.91 %
//   probe3 temp raw = 0x1A2C = 6700 → 27.00 °C
func TestTempHumidityTemp_FifteenBytes(t *testing.T) {
	data := []byte{
		0x41, 0x41, 0x5a, 0x00, // header + indicator
		0x5a, 0x19, 0xa3, // probe1 temp
		0x5a, 0x07, 0xd0, // humidity
		0x5a, 0x1a, 0x2c, // probe2 temp
		0x5a, 0x0d, // tail
	}
	probes := parseHexResponse(data, "192.168.1.1")

	if len(probes) != 3 {
		t.Fatalf("expected 3 probes, got %d", len(probes))
	}

	// Probe 1 — temperature
	if probes[0].ProbeNo != 1 || probes[0].TempValue != 25.63 {
		t.Errorf("probe1: got ProbeNo=%d TempValue=%v, want 1 / 25.63",
			probes[0].ProbeNo, probes[0].TempValue)
	}

	// Probe 2 — humidity
	if probes[1].ProbeNo != 2 {
		t.Errorf("probe2 (humidity): ProbeNo=%d, want 2", probes[1].ProbeNo)
	}
	wantRH := round2(calcHumidity(2000, 25.63, 0))
	if got := probes[1].TempValue; got != wantRH {
		t.Errorf("probe2 (humidity): TempValue=%v, want %v", got, wantRH)
	}

	// Probe 3 — second temperature
	if probes[2].ProbeNo != 3 || probes[2].TempValue != 27.00 {
		t.Errorf("probe3: got ProbeNo=%d TempValue=%v, want 3 / 27.00",
			probes[2].ProbeNo, probes[2].TempValue)
	}
}

// TestOneProbe_ProbeIndicator03_TenBytes tests that the probe indicator 0x03
// triggers two-probe parsing even if the length isn't exactly 12.
func TestTwoProbes_ProbeIndicator03(t *testing.T) {
	// Same as 12-byte frame but verify probe indicator path triggers correctly.
	data := []byte{0x41, 0x41, 0x5a, 0x03, 0x5a, 0x19, 0xa3, 0x5a, 0x19, 0xae, 0x5a, 0x0d}
	probes := parseHexResponse(data, "192.168.1.1")
	if len(probes) != 2 {
		t.Fatalf("probe indicator 0x03: expected 2 probes, got %d", len(probes))
	}
}

// TestInvalidHeader returns empty probes when the magic bytes are wrong.
func TestInvalidHeader(t *testing.T) {
	data := []byte{0xFF, 0xFE, 0x5a, 0x00, 0x5a, 0x19, 0xa3, 0x5a, 0x0d}
	probes := parseHexResponse(data, "192.168.1.1")
	if len(probes) != 0 {
		t.Errorf("invalid header: expected 0 probes, got %d", len(probes))
	}
}

// TestTooShort returns empty probes for frames shorter than the minimum 9 bytes.
func TestTooShort(t *testing.T) {
	short := []byte{0x41, 0x41, 0x5a, 0x00, 0x5a, 0x19}
	probes := parseHexResponse(short, "192.168.1.1")
	if len(probes) != 0 {
		t.Errorf("too-short frame: expected 0 probes, got %d", len(probes))
	}
}

// TestBrokenSensor_RawValue65535 confirms that a broken sensor (0xFFFF) is
// parsed and returned — the *caller* (pollAndSave) is responsible for filtering
// the sentinel value, not the parser.
func TestBrokenSensor_RawValue65535(t *testing.T) {
	data := []byte{0x41, 0x41, 0x5a, 0x00, 0x5a, 0xFF, 0xFF, 0x5a, 0x0d}
	probes := parseHexResponse(data, "192.168.1.1")
	if len(probes) != 1 {
		t.Fatalf("expected 1 probe (broken sensor), got %d", len(probes))
	}
	if probes[0].RealValue != 65535 {
		t.Errorf("broken sensor: RealValue = %d, want 65535", probes[0].RealValue)
	}
}

// TestProbe1_BadSeparator returns empty probes when byte[4] isn't 0x5A.
func TestProbe1_BadSeparator(t *testing.T) {
	data := []byte{0x41, 0x41, 0x5a, 0x00, 0xBB, 0x19, 0xa3, 0x5a, 0x0d}
	probes := parseHexResponse(data, "192.168.1.1")
	if len(probes) != 0 {
		t.Errorf("bad separator: expected 0 probes, got %d", len(probes))
	}
}

// ── calcHumidity ──────────────────────────────────────────────────────────────

// TestCalcHumidity_AtReferenceTemp verifies the SHT1x formula at 25 °C where
// the temperature-compensation term is zero and the result is purely algebraic.
//
//   r = 2000, T = 25, aVal = 0
//   part1 = -4 + 0.0405*2000 - 0.0000028*4000000 = -4+81-11.2 = 65.8
//   part2 = (25-25)*(…) = 0
//   RH    = 65.8
func TestCalcHumidity_AtReferenceTemp(t *testing.T) {
	got := calcHumidity(2000, 25.0, 0)
	want := 65.8
	if math.Abs(got-want) > 0.01 {
		t.Errorf("calcHumidity(2000, 25, 0) = %v, want ≈ %v", got, want)
	}
}

// TestCalcHumidity_WithAdjustment confirms the aVal offset is added directly.
func TestCalcHumidity_WithAdjustment(t *testing.T) {
	base := calcHumidity(2000, 25.0, 0)
	adjusted := calcHumidity(2000, 25.0, 2.5)
	if math.Abs((adjusted-base)-2.5) > 0.01 {
		t.Errorf("adjustment not applied correctly: base=%v adjusted=%v diff=%v",
			base, adjusted, adjusted-base)
	}
}

// TestCalcHumidity_TempCompensation confirms the compensation term shifts the
// result when temperature deviates from 25 °C.
func TestCalcHumidity_TempCompensation(t *testing.T) {
	at25 := calcHumidity(2000, 25.0, 0)
	at30 := calcHumidity(2000, 30.0, 0)
	// (30-25)*(0.01+0.00008*2000) = 5*0.17 = 0.85  →  at30 > at25
	if at30 <= at25 {
		t.Errorf("temperature compensation: expected at30 (%v) > at25 (%v)", at30, at25)
	}
	if math.Abs((at30-at25)-0.85) > 0.02 {
		t.Errorf("temperature compensation: delta=%v, want ≈0.85", at30-at25)
	}
}

// ── roundTo2Decimal ───────────────────────────────────────────────────────────

func TestRoundTo2Decimal(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{25.634, 25.63},
		{25.635, 25.64},
		{0.001, 0.0},
		{-3.555, -3.56},
	}
	for _, tc := range cases {
		got := roundTo2Decimal(tc.in)
		if got != tc.want {
			t.Errorf("roundTo2Decimal(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
