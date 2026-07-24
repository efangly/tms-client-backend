//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"tms-backend/internal/models"
)

// TestPollingWritesTempLog exercises the full real path: POST /api/poll ->
// PollingService.pollAndSave -> real TCP dial to the fake probe server ->
// real wire-protocol parse -> real DB write, then confirms it's readable
// back through /api/temp-logs and /api/reports/templog.
func TestPollingWritesTempLog(t *testing.T) {
	truncateAll(t)
	probe.SetTemp(21.34)

	createDevice(t, map[string]any{
		"machineIp": "127.0.0.1", "probeNo": 1, "machineName": "Fake Probe",
		"sType": "t", "minTemp": 2.0, "maxTemp": 30.0,
	})

	resp, body := doRequest(t, http.MethodPost, "/api/poll", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trigger poll: status = %d, body = %s", resp.StatusCode, body)
	}

	// TriggerPoll runs pollAndSave in a background goroutine, so wait for the
	// row to actually land.
	var logs []models.TempLog
	waitFor(t, 5*time.Second, func() bool {
		_, body := doRequest(t, http.MethodGet, "/api/temp-logs?limit=10", nil)
		logs = nil
		if err := json.Unmarshal(body, &logs); err != nil {
			return false
		}
		return len(logs) > 0
	})

	if len(logs) != 1 {
		t.Fatalf("temp_logs = %d rows, want 1: %+v", len(logs), logs)
	}
	got := logs[0]
	if got.MachineIP != "127.0.0.1" || got.ProbeNo != 1 {
		t.Errorf("log ip/probe = %s/%d, want 127.0.0.1/1", got.MachineIP, got.ProbeNo)
	}
	if got.TempValue == nil || *got.TempValue != 21.34 {
		t.Errorf("log TempValue = %v, want 21.34", got.TempValue)
	}

	// The report endpoint reshapes the same row into a chart series.
	today := time.Now().Format("2006-01-02")
	resp, body = doRequest(t, http.MethodGet, "/api/reports/templog?startDate="+today+"&endDate="+today, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report: status = %d, body = %s", resp.StatusCode, body)
	}
	var report struct {
		Data   []models.TempLog `json:"data"`
		Series []struct {
			Label string `json:"label"`
			Data  []struct {
				Y float64 `json:"y"`
			} `json:"data"`
		} `json:"series"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.Data) != 1 {
		t.Fatalf("report data = %d rows, want 1", len(report.Data))
	}
	if len(report.Series) != 1 || len(report.Series[0].Data) != 1 || report.Series[0].Data[0].Y != 21.34 {
		t.Errorf("report series = %+v, want one series with one point at 21.34", report.Series)
	}
}
