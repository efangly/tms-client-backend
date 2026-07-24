//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"tms-backend/internal/models"
)

func TestDeviceLifecycle(t *testing.T) {
	truncateAll(t)

	create := map[string]any{
		"machineIp":   "10.20.30.1",
		"probeNo":     1,
		"machineName": "E2E Device",
		"sType":       "t",
		"minTemp":     2.0,
		"maxTemp":     8.0,
	}
	resp, body := doRequest(t, http.MethodPost, "/api/devices", create)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", resp.StatusCode, body)
	}

	// GET — created device is readable back with the fields we sent.
	resp, body = doRequest(t, http.MethodGet, "/api/devices/10.20.30.1?probeNo=1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status = %d, body = %s", resp.StatusCode, body)
	}
	var got models.MasterMachine
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	if got.MachineName != "E2E Device" || got.GetMinTemp() != 2.0 || got.GetMaxTemp() != 8.0 {
		t.Errorf("get returned %+v, want name=E2E Device min=2 max=8", got)
	}

	// UPDATE — a partial update only changes the given field.
	resp, body = doRequest(t, http.MethodPut, "/api/devices/10.20.30.1?probeNo=1", map[string]any{
		"machineName": "E2E Device Updated",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d, body = %s", resp.StatusCode, body)
	}

	// Re-fetch (don't trust the write response) to confirm it persisted.
	resp, body = doRequest(t, http.MethodGet, "/api/devices/10.20.30.1?probeNo=1", nil)
	json.Unmarshal(body, &got)
	if got.MachineName != "E2E Device Updated" {
		t.Errorf("after update, machineName = %q, want %q", got.MachineName, "E2E Device Updated")
	}
	if got.GetMinTemp() != 2.0 {
		t.Errorf("after partial update, minTemp = %v, want unchanged 2.0", got.GetMinTemp())
	}

	// LIST — appears in the full device listing.
	resp, body = doRequest(t, http.MethodGet, "/api/devices", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", resp.StatusCode, body)
	}
	var list []models.MasterMachine
	json.Unmarshal(body, &list)
	found := false
	for _, d := range list {
		if d.MachineIP == "10.20.30.1" && d.ProbeNo == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("device list = %+v, want it to contain 10.20.30.1/probe1", list)
	}

	// DELETE
	resp, body = doRequest(t, http.MethodDelete, "/api/devices/10.20.30.1?probeNo=1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: status = %d, body = %s", resp.StatusCode, body)
	}

	// GET after delete -> 404
	resp, _ = doRequest(t, http.MethodGet, "/api/devices/10.20.30.1?probeNo=1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete: status = %d, want 404", resp.StatusCode)
	}
}
