//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"tms-backend/internal/models"
)

// TestPollingTriggersAlert drives a real out-of-range reading through
// PollingService.pollAndSave -> checkProbeAlert and confirms a temp_error
// row is created, exercising the alert state machine against the real DB
// (not sqlmock, unlike internal/services/polling_test.go's unit tests).
//
// Uses 127.0.0.2 (a distinct loopback identity from the polling test's
// 127.0.0.1) so this test's in-memory alert-state key starts fresh
// regardless of test execution order.
func TestPollingTriggersAlert(t *testing.T) {
	truncateAll(t)
	probe.SetTemp(35.0) // above maxTemp below

	createDevice(t, map[string]any{
		"machineIp": "127.0.0.2", "probeNo": 1, "machineName": "Alerting Probe",
		"sType": "t", "minTemp": 2.0, "maxTemp": 8.0,
	})

	resp, body := doRequest(t, http.MethodPost, "/api/poll", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trigger poll: status = %d, body = %s", resp.StatusCode, body)
	}

	var errs []models.TempError
	waitFor(t, 5*time.Second, func() bool {
		_, body := doRequest(t, http.MethodGet, "/api/temp-errors", nil)
		errs = nil
		if err := json.Unmarshal(body, &errs); err != nil {
			return false
		}
		return len(errs) > 0
	})

	if len(errs) != 1 {
		t.Fatalf("temp_errors = %d rows, want 1: %+v", len(errs), errs)
	}
	got := errs[0]
	if got.MachineIP != "127.0.0.2" || got.ProbeNo != 1 {
		t.Errorf("error ip/probe = %s/%d, want 127.0.0.2/1", got.MachineIP, got.ProbeNo)
	}
	if got.ErrorType != "o" {
		t.Errorf("errorType = %q, want %q (over-range)", got.ErrorType, "o")
	}
	if got.TempValue == nil || *got.TempValue != 35.0 {
		t.Errorf("error TempValue = %v, want 35.0", got.TempValue)
	}
}
