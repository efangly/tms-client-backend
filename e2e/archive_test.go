//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/services"
)

// TestArchiveRunAndRestore drives the full archive lifecycle over HTTP: a
// temp_log row older than the retention window (30 days, the package
// default — see .env.e2e.example's note on init()-time env vars) gets
// archived to a real JSON file on disk and removed from temp_log, then
// restored back into temp_log_archive.
func TestArchiveRunAndRestore(t *testing.T) {
	truncateAll(t)

	oldTime := database.GetThailandTime().AddDate(0, 0, -35)
	day := oldTime.Format("2006-01-02")
	temp := 4.5
	real := 450
	status := "N"
	sDate := oldTime.Format("20060102")
	sTime := oldTime.Format("15")
	row := models.TempLog{
		MachineIP: "10.20.30.5", ProbeNo: 1,
		TempValue: &temp, RealValue: &real, Status: &status,
		SendTime: &oldTime, InsertTime: oldTime, SDate: &sDate, STime: &sTime,
	}
	if err := database.DB.Create(&row).Error; err != nil {
		t.Fatalf("insert old temp_log row: %v", err)
	}

	// Run the archive.
	resp, body := doRequest(t, http.MethodPost, "/api/archive/run", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archive/run: status = %d, body = %s", resp.StatusCode, body)
	}
	var runResult struct {
		DaysArchived int `json:"daysArchived"`
		RowsArchived int `json:"rowsArchived"`
	}
	if err := json.Unmarshal(body, &runResult); err != nil {
		t.Fatalf("unmarshal archive/run result: %v", err)
	}
	if runResult.DaysArchived != 1 || runResult.RowsArchived != 1 {
		t.Fatalf("archive/run result = %+v, want 1 day / 1 row", runResult)
	}

	// The JSON file was actually written to disk with the archived row.
	wantFile := filepath.Join(services.ArchiveDir(), "temp_log", oldTime.Format("2006"), oldTime.Format("01"), "temp_log_"+day+".json")
	data, err := os.ReadFile(wantFile)
	if err != nil {
		t.Fatalf("archive file not found at %s: %v", wantFile, err)
	}
	var archived []models.TempLog
	if err := json.Unmarshal(data, &archived); err != nil {
		t.Fatalf("unmarshal archive file: %v", err)
	}
	if len(archived) != 1 || archived[0].MachineIP != "10.20.30.5" {
		t.Fatalf("archive file contents = %+v, want 1 row for 10.20.30.5", archived)
	}

	// The manifest is listed via the API.
	resp, body = doRequest(t, http.MethodGet, "/api/archive?startDate="+day+"&endDate="+day, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get archive manifests: status = %d, body = %s", resp.StatusCode, body)
	}
	var manifests []models.ArchiveManifest
	json.Unmarshal(body, &manifests)
	if len(manifests) != 1 || manifests[0].PeriodDate != day || manifests[0].RowCount != 1 {
		t.Fatalf("manifests = %+v, want one entry for %s with rowCount 1", manifests, day)
	}

	// The row is gone from the live temp_log table.
	resp, body = doRequest(t, http.MethodGet, "/api/temp-logs?startDate="+day+"&endDate="+day+"&limit=100", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get temp-logs: status = %d, body = %s", resp.StatusCode, body)
	}
	var remaining []models.TempLog
	json.Unmarshal(body, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("temp_log rows remaining after archive = %+v, want none", remaining)
	}

	// Restore brings it back into temp_log_archive.
	resp, body = doRequest(t, http.MethodPost, "/api/archive/restore?startDate="+day+"&endDate="+day, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archive/restore: status = %d, body = %s", resp.StatusCode, body)
	}
	var restoreResult struct {
		DaysRestored int `json:"daysRestored"`
		RowsRestored int `json:"rowsRestored"`
	}
	if err := json.Unmarshal(body, &restoreResult); err != nil {
		t.Fatalf("unmarshal archive/restore result: %v", err)
	}
	if restoreResult.DaysRestored != 1 || restoreResult.RowsRestored != 1 {
		t.Fatalf("archive/restore result = %+v, want 1 day / 1 row", restoreResult)
	}

	// No read endpoint exists for temp_log_archive — check the real DB directly.
	var restoredRows []models.TempLogArchive
	if err := database.DB.Where("machine_ip = ?", "10.20.30.5").Find(&restoredRows).Error; err != nil {
		t.Fatalf("query temp_log_archive: %v", err)
	}
	if len(restoredRows) != 1 || restoredRows[0].TempValue == nil || *restoredRows[0].TempValue != 4.5 {
		t.Fatalf("temp_log_archive rows = %+v, want one row with TempValue 4.5", restoredRows)
	}
}
