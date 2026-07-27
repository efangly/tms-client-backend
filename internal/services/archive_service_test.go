package services

// White-box tests for ArchiveService — same package so we can override the
// package-level archiveDir/archiveRunHour config vars and exercise unexported
// helpers (writeArchiveFile, verifyArchiveFile, runIfDue).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/testutil"
)

func floatPtr(f float64) *float64 { return &f }

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

// withTempArchiveDir points the package-level archiveDir at a fresh temp
// directory for the duration of the test and restores it afterward.
func withTempArchiveDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := archiveDir
	archiveDir = dir
	t.Cleanup(func() { archiveDir = prev })
	return dir
}

// ── writeArchiveFile / verifyArchiveFile ──────────────────────────────────────

func TestWriteArchiveFile_WritesExpectedPath(t *testing.T) {
	withTempArchiveDir(t)
	rows := []models.TempLog{{
		MachineIP:  "192.168.1.10",
		ProbeNo:    1,
		TempValue:  floatPtr(25.5),
		InsertTime: mustParseTime(t, "2026-01-15 08:00:00"),
	}}

	path, err := writeArchiveFile("temp_log", "2026-01-15", rows)
	if err != nil {
		t.Fatalf("writeArchiveFile: %v", err)
	}

	wantSuffix := filepath.Join("temp_log", "2026", "01", "temp_log_2026-01-15.json")
	if !strings.HasSuffix(path, wantSuffix) {
		t.Errorf("path = %s, want suffix %s", path, wantSuffix)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read written file: %v", err)
	}
	var got []models.TempLog
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal written file: %v", err)
	}
	if len(got) != 1 || got[0].MachineIP != "192.168.1.10" {
		t.Errorf("written rows = %+v", got)
	}
}

func TestWriteArchiveFile_InvalidDay_ReturnsError(t *testing.T) {
	withTempArchiveDir(t)
	if _, err := writeArchiveFile("temp_log", "not-a-date", []models.TempLog{}); err == nil {
		t.Error("expected error for invalid day format")
	}
}

func TestVerifyArchiveFile_MatchingCount_NoError(t *testing.T) {
	withTempArchiveDir(t)
	rows := []models.TempLog{
		{MachineIP: "192.168.1.10", ProbeNo: 1, InsertTime: mustParseTime(t, "2026-01-15 08:00:00")},
		{MachineIP: "192.168.1.10", ProbeNo: 2, InsertTime: mustParseTime(t, "2026-01-15 08:05:00")},
	}
	path, err := writeArchiveFile("temp_log", "2026-01-15", rows)
	if err != nil {
		t.Fatalf("writeArchiveFile: %v", err)
	}

	if err := verifyArchiveFile(path, 2); err != nil {
		t.Errorf("verifyArchiveFile: %v", err)
	}
}

func TestVerifyArchiveFile_CountMismatch_ReturnsError(t *testing.T) {
	withTempArchiveDir(t)
	rows := []models.TempLog{{MachineIP: "192.168.1.10", ProbeNo: 1, InsertTime: mustParseTime(t, "2026-01-15 08:00:00")}}
	path, err := writeArchiveFile("temp_log", "2026-01-15", rows)
	if err != nil {
		t.Fatalf("writeArchiveFile: %v", err)
	}

	if err := verifyArchiveFile(path, 2); err == nil {
		t.Error("expected row-count mismatch error")
	}
}

func TestVerifyArchiveFile_MissingFile_ReturnsError(t *testing.T) {
	withTempArchiveDir(t)
	if err := verifyArchiveFile(filepath.Join(archiveDir, "does-not-exist.json"), 1); err == nil {
		t.Error("expected error for missing file")
	}
}

// ── RunArchive ────────────────────────────────────────────────────────────────

func TestRunArchive_ArchivesUnarchivedDay(t *testing.T) {
	withTempArchiveDir(t)
	mock := testutil.SetupMockDB(t)

	day := "2026-01-10"
	mock.ExpectQuery(`SELECT DISTINCT DATE_FORMAT`).
		WillReturnRows(sqlmock.NewRows([]string{"day"}).AddRow(day))

	// Manifest lookup finds nothing -> day is eligible for archiving.
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).
		WillReturnRows(sqlmock.NewRows(testutil.ArchiveManifestColumns))

	ts := mustParseTime(t, day+" 08:00:00")
	logRows := sqlmock.NewRows(testutil.TempLogColumns).
		AddRow("192.168.1.10", 1, nil, 25.5, nil, nil, nil, ts, nil, nil)
	mock.ExpectQuery(`SELECT \* FROM .temp_log.`).WillReturnRows(logRows)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM .temp_log.`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO .archive_manifest.`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	svc := NewArchiveService()
	result, err := svc.RunArchive()
	if err != nil {
		t.Fatalf("RunArchive: %v", err)
	}
	if result.DaysArchived != 1 || result.RowsArchived != 1 {
		t.Errorf("result = %+v, want {DaysArchived:1 RowsArchived:1}", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet DB expectations: %v", err)
	}

	filePath := filepath.Join(archiveDir, "temp_log", "2026", "01", "temp_log_2026-01-10.json")
	if _, err := os.Stat(filePath); err != nil {
		t.Errorf("expected archive file at %s: %v", filePath, err)
	}
}

func TestRunArchive_SkipsAlreadyArchivedDay(t *testing.T) {
	withTempArchiveDir(t)
	mock := testutil.SetupMockDB(t)

	day := "2026-01-10"
	mock.ExpectQuery(`SELECT DISTINCT DATE_FORMAT`).
		WillReturnRows(sqlmock.NewRows([]string{"day"}).AddRow(day))

	existing := sqlmock.NewRows(testutil.ArchiveManifestColumns).
		AddRow(1, "temp_log", day, "archives/temp_log/2026/01/temp_log_2026-01-10.json", 5, time.Now())
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).WillReturnRows(existing)

	// No further queries expected: no temp_log SELECT/DELETE, no transaction.
	svc := NewArchiveService()
	result, err := svc.RunArchive()
	if err != nil {
		t.Fatalf("RunArchive: %v", err)
	}
	if result.DaysArchived != 0 || result.RowsArchived != 0 {
		t.Errorf("result = %+v, want zero (day already archived)", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet DB expectations: %v", err)
	}
}

func TestRunArchive_NoArchivableDays_ReturnsZero(t *testing.T) {
	withTempArchiveDir(t)
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT DISTINCT DATE_FORMAT`).
		WillReturnRows(sqlmock.NewRows([]string{"day"}))

	svc := NewArchiveService()
	result, err := svc.RunArchive()
	if err != nil {
		t.Fatalf("RunArchive: %v", err)
	}
	if result.DaysArchived != 0 || result.RowsArchived != 0 {
		t.Errorf("result = %+v, want zero", result)
	}
}

func TestRunArchive_DayListQueryError_ReturnsError(t *testing.T) {
	withTempArchiveDir(t)
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT DISTINCT DATE_FORMAT`).WillReturnError(errArchiveDB)

	svc := NewArchiveService()
	if _, err := svc.RunArchive(); err == nil {
		t.Error("expected error when day-listing query fails")
	}
}

// ── RestoreRange ──────────────────────────────────────────────────────────────

func TestRestoreRange_LoadsArchivedFileIntoTable(t *testing.T) {
	withTempArchiveDir(t)
	mock := testutil.SetupMockDB(t)

	day := "2026-01-10"
	archived := []models.TempLogArchive{{
		MachineIP:  "192.168.1.10",
		ProbeNo:    1,
		TempValue:  floatPtr(25.5),
		InsertTime: mustParseTime(t, day+" 08:00:00"),
	}}
	data, err := json.Marshal(archived)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	dir := filepath.Join(archiveDir, "temp_log", "2026", "01")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	filePath := filepath.Join(dir, "temp_log_2026-01-10.json")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	manifestRows := sqlmock.NewRows(testutil.ArchiveManifestColumns).
		AddRow(1, "temp_log", day, filePath, 1, time.Now())
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).WillReturnRows(manifestRows)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM .temp_log_archive.`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO .temp_log_archive.`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	svc := NewArchiveService()
	result, err := svc.RestoreRange(day, day)
	if err != nil {
		t.Fatalf("RestoreRange: %v", err)
	}
	if result.DaysRestored != 1 || result.RowsRestored != 1 {
		t.Errorf("result = %+v, want {DaysRestored:1 RowsRestored:1}", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet DB expectations: %v", err)
	}
}

func TestRestoreRange_MissingFile_SkipsWithoutError(t *testing.T) {
	withTempArchiveDir(t)
	mock := testutil.SetupMockDB(t)

	manifestRows := sqlmock.NewRows(testutil.ArchiveManifestColumns).
		AddRow(1, "temp_log", "2026-01-10", filepath.Join(archiveDir, "missing.json"), 1, time.Now())
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).WillReturnRows(manifestRows)

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM .temp_log_archive.`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	svc := NewArchiveService()
	result, err := svc.RestoreRange("2026-01-10", "2026-01-10")
	if err != nil {
		t.Fatalf("RestoreRange: %v", err)
	}
	if result.DaysRestored != 0 || result.RowsRestored != 0 {
		t.Errorf("result = %+v, want zero for missing file", result)
	}
}

func TestRestoreRange_NoManifests_ReturnsZero(t *testing.T) {
	withTempArchiveDir(t)
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).
		WillReturnRows(sqlmock.NewRows(testutil.ArchiveManifestColumns))

	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM .temp_log_archive.`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	svc := NewArchiveService()
	result, err := svc.RestoreRange("2026-01-01", "2026-01-31")
	if err != nil {
		t.Fatalf("RestoreRange: %v", err)
	}
	if result.DaysRestored != 0 || result.RowsRestored != 0 {
		t.Errorf("result = %+v, want zero", result)
	}
}

func TestRestoreRange_ClearsPreviousDataOnEachCall(t *testing.T) {
	withTempArchiveDir(t)
	mock := testutil.SetupMockDB(t)

	day := "2026-01-10"
	archived := []models.TempLogArchive{{
		MachineIP:  "192.168.1.10",
		ProbeNo:    1,
		TempValue:  floatPtr(25.5),
		InsertTime: mustParseTime(t, day+" 08:00:00"),
	}}
	data, err := json.Marshal(archived)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	dir := filepath.Join(archiveDir, "temp_log", "2026", "01")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	filePath := filepath.Join(dir, "temp_log_2026-01-10.json")
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	manifestRows := sqlmock.NewRows(testutil.ArchiveManifestColumns).
		AddRow(1, "temp_log", day, filePath, 1, time.Now())

	svc := NewArchiveService()

	// First call: finds the manifest, clears the (empty) table, then inserts.
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).WillReturnRows(manifestRows)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM .temp_log_archive.`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO .temp_log_archive.`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if _, err := svc.RestoreRange(day, day); err != nil {
		t.Fatalf("first RestoreRange: %v", err)
	}

	// Second call for a range with no manifests: must still clear the table,
	// leaving it empty rather than holding the first call's row.
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).
		WillReturnRows(sqlmock.NewRows(testutil.ArchiveManifestColumns))
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM .temp_log_archive.`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err := svc.RestoreRange("2026-02-01", "2026-02-28")
	if err != nil {
		t.Fatalf("second RestoreRange: %v", err)
	}
	if result.DaysRestored != 0 || result.RowsRestored != 0 {
		t.Errorf("result = %+v, want zero for a range with no manifests", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet DB expectations: %v", err)
	}
}

// ── runIfDue ──────────────────────────────────────────────────────────────────

func TestRunIfDue_NotYetDue_NoDBCallsMade(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	prevHour := archiveRunHour
	archiveRunHour = 24 // Hour() is always 0-23, so this is never "due".
	t.Cleanup(func() { archiveRunHour = prevHour })

	svc := NewArchiveService()
	svc.runIfDue()

	if svc.lastRunDate != "" {
		t.Errorf("lastRunDate = %q, want empty (archive should not have run)", svc.lastRunDate)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected DB calls: %v", err)
	}
}

func TestRunIfDue_AlreadyRanToday_NoDBCallsMade(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	prevHour := archiveRunHour
	archiveRunHour = 0 // always "due" by hour
	t.Cleanup(func() { archiveRunHour = prevHour })

	svc := NewArchiveService()
	svc.lastRunDate = database.GetThailandTime().Format("2006-01-02")

	svc.runIfDue()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected DB calls: %v", err)
	}
}

func TestRunIfDue_Due_RunsArchiveAndRecordsToday(t *testing.T) {
	withTempArchiveDir(t)
	mock := testutil.SetupMockDB(t)
	prevHour := archiveRunHour
	archiveRunHour = 0
	t.Cleanup(func() { archiveRunHour = prevHour })

	mock.ExpectQuery(`SELECT DISTINCT DATE_FORMAT`).
		WillReturnRows(sqlmock.NewRows([]string{"day"}))

	svc := NewArchiveService()
	svc.runIfDue()

	want := database.GetThailandTime().Format("2006-01-02")
	if svc.lastRunDate != want {
		t.Errorf("lastRunDate = %q, want %q", svc.lastRunDate, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet DB expectations: %v", err)
	}
}

// errArchiveDB is a sentinel error simulating a generic DB failure.
var errArchiveDB = archiveErrorString("simulated database error")

type archiveErrorString string

func (e archiveErrorString) Error() string { return string(e) }
