package models_test

import (
	"testing"

	"tms-backend/internal/models"
)

// ── MasterMachine helper methods ──────────────────────────────────────────────

func TestGetMinTemp_ReturnsPointerValue(t *testing.T) {
	v := 15.5
	m := models.MasterMachine{MinTemp: &v}
	if got := m.GetMinTemp(); got != 15.5 {
		t.Errorf("GetMinTemp() = %v, want 15.5", got)
	}
}

func TestGetMinTemp_NilReturnsZero(t *testing.T) {
	m := models.MasterMachine{}
	if got := m.GetMinTemp(); got != 0 {
		t.Errorf("GetMinTemp() on nil = %v, want 0", got)
	}
}

func TestGetMaxTemp_ReturnsPointerValue(t *testing.T) {
	v := 35.0
	m := models.MasterMachine{MaxTemp: &v}
	if got := m.GetMaxTemp(); got != 35.0 {
		t.Errorf("GetMaxTemp() = %v, want 35.0", got)
	}
}

func TestGetMaxTemp_NilReturns100(t *testing.T) {
	m := models.MasterMachine{}
	if got := m.GetMaxTemp(); got != 100 {
		t.Errorf("GetMaxTemp() on nil = %v, want 100", got)
	}
}

func TestGetAdjTemp_ReturnsPointerValue(t *testing.T) {
	v := -1.5
	m := models.MasterMachine{AdjTemp: &v}
	if got := m.GetAdjTemp(); got != -1.5 {
		t.Errorf("GetAdjTemp() = %v, want -1.5", got)
	}
}

func TestGetAdjTemp_NilReturnsZero(t *testing.T) {
	m := models.MasterMachine{}
	if got := m.GetAdjTemp(); got != 0 {
		t.Errorf("GetAdjTemp() on nil = %v, want 0", got)
	}
}

// ── Sensor type predicates ────────────────────────────────────────────────────

func TestIsTemperatureType(t *testing.T) {
	cases := []struct {
		stype string
		want  bool
	}{
		{"t", true},
		{"", true},  // empty defaults to temperature
		{"h", false},
		{"p", false},
	}
	for _, tc := range cases {
		m := models.MasterMachine{SType: tc.stype}
		if got := m.IsTemperatureType(); got != tc.want {
			t.Errorf("IsTemperatureType(%q) = %v, want %v", tc.stype, got, tc.want)
		}
	}
}

func TestIsHumidityType(t *testing.T) {
	cases := []struct {
		stype string
		want  bool
	}{
		{"h", true},
		{"t", false},
		{"p", false},
		{"", false},
	}
	for _, tc := range cases {
		m := models.MasterMachine{SType: tc.stype}
		if got := m.IsHumidityType(); got != tc.want {
			t.Errorf("IsHumidityType(%q) = %v, want %v", tc.stype, got, tc.want)
		}
	}
}

func TestIsPowerType(t *testing.T) {
	cases := []struct {
		stype string
		want  bool
	}{
		{"p", true},
		{"t", false},
		{"h", false},
		{"", false},
	}
	for _, tc := range cases {
		m := models.MasterMachine{SType: tc.stype}
		if got := m.IsPowerType(); got != tc.want {
			t.Errorf("IsPowerType(%q) = %v, want %v", tc.stype, got, tc.want)
		}
	}
}

// ── GetTypeLabel ──────────────────────────────────────────────────────────────

func TestGetTypeLabel(t *testing.T) {
	cases := []struct {
		stype string
		want  string
	}{
		{"t", "Temperature"},
		{"", "Temperature"},
		{"h", "Humidity"},
		{"p", "Power"},
	}
	for _, tc := range cases {
		m := models.MasterMachine{SType: tc.stype}
		if got := m.GetTypeLabel(); got != tc.want {
			t.Errorf("GetTypeLabel(%q) = %q, want %q", tc.stype, got, tc.want)
		}
	}
}

// ── GetUnit ───────────────────────────────────────────────────────────────────

func TestGetUnit(t *testing.T) {
	cases := []struct {
		stype string
		want  string
	}{
		{"t", "°C"},
		{"", "°C"},
		{"h", "%"},
		{"p", "W"},
	}
	for _, tc := range cases {
		m := models.MasterMachine{SType: tc.stype}
		if got := m.GetUnit(); got != tc.want {
			t.Errorf("GetUnit(%q) = %q, want %q", tc.stype, got, tc.want)
		}
	}
}

// ── TableName ─────────────────────────────────────────────────────────────────

func TestTableNames(t *testing.T) {
	if got := (models.MasterMachine{}).TableName(); got != "master_machine" {
		t.Errorf("MasterMachine.TableName() = %q", got)
	}
	if got := (models.TempLog{}).TableName(); got != "temp_log" {
		t.Errorf("TempLog.TableName() = %q", got)
	}
	if got := (models.TempError{}).TableName(); got != "temp_error" {
		t.Errorf("TempError.TableName() = %q", got)
	}
	if got := (models.ConfigValue{}).TableName(); got != "config_value" {
		t.Errorf("ConfigValue.TableName() = %q", got)
	}
	if got := (models.MasterUser{}).TableName(); got != "master_user" {
		t.Errorf("MasterUser.TableName() = %q", got)
	}
}
