//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
)

// TestMachinesOnlineStatus verifies that GET /api/machines derives Online vs
// Offline status from the real most-recent temp_log row per machine/probe,
// via the real SQL join in handlers.GetMachines.
func TestMachinesOnlineStatus(t *testing.T) {
	truncateAll(t)

	createDevice(t, map[string]any{
		"machineIp": "10.20.30.2", "probeNo": 1, "machineName": "Online Probe", "sType": "t",
	})
	createDevice(t, map[string]any{
		"machineIp": "10.20.30.3", "probeNo": 1, "machineName": "Offline Probe", "sType": "t",
	})

	temp := 5.0
	real := 500
	status := "N"
	now := time.Now()
	sDate := now.Format("20060102")
	sTime := now.Format("15")
	tempLog := models.TempLog{
		MachineIP: "10.20.30.2", ProbeNo: 1,
		TempValue: &temp, RealValue: &real, Status: &status,
		SendTime: &now, InsertTime: now, SDate: &sDate, STime: &sTime,
	}
	if err := database.DB.Create(&tempLog).Error; err != nil {
		t.Fatalf("insert temp_log: %v", err)
	}

	resp, body := doRequest(t, http.MethodGet, "/api/machines", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var machines []models.MachineWithStatus
	if err := json.Unmarshal(body, &machines); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	statusFor := make(map[string]string)
	for _, m := range machines {
		statusFor[m.MachineIP] = m.OnlineStatus
	}
	if statusFor["10.20.30.2"] != "Online" {
		t.Errorf("10.20.30.2 status = %q, want Online", statusFor["10.20.30.2"])
	}
	if statusFor["10.20.30.3"] != "Offline" {
		t.Errorf("10.20.30.3 status = %q, want Offline", statusFor["10.20.30.3"])
	}
}
