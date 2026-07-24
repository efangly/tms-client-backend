//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func getScheduleTimes(t *testing.T, body []byte) []string {
	t.Helper()
	var got struct {
		Times []string `json:"times"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal schedule response: %v (body=%s)", err, body)
	}
	return got.Times
}

// TestScheduleWorkflow exercises the full set/add/remove/get cycle for
// schedule times, which are persisted in master_machine.color.
func TestScheduleWorkflow(t *testing.T) {
	truncateAll(t)
	createDevice(t, map[string]any{
		"machineIp": "10.20.30.4", "probeNo": 1, "machineName": "Sched Device", "sType": "t",
	})

	resp, body := doRequest(t, http.MethodPut, "/api/machines/10.20.30.4/1/schedule", map[string]any{
		"times": []string{"0800", "1200"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set schedule: status = %d, body = %s", resp.StatusCode, body)
	}

	resp, body = doRequest(t, http.MethodGet, "/api/machines/10.20.30.4/1/schedule", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get schedule: status = %d, body = %s", resp.StatusCode, body)
	}
	if got := getScheduleTimes(t, body); !reflect.DeepEqual(got, []string{"0800", "1200"}) {
		t.Fatalf("times after set = %v, want [0800 1200]", got)
	}

	resp, body = doRequest(t, http.MethodPost, "/api/machines/10.20.30.4/1/schedule/1600", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add schedule time: status = %d, body = %s", resp.StatusCode, body)
	}
	if got := getScheduleTimes(t, body); !reflect.DeepEqual(got, []string{"0800", "1200", "1600"}) {
		t.Fatalf("times after add = %v, want [0800 1200 1600]", got)
	}

	resp, body = doRequest(t, http.MethodDelete, "/api/machines/10.20.30.4/1/schedule/0800", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove schedule time: status = %d, body = %s", resp.StatusCode, body)
	}
	if got := getScheduleTimes(t, body); !reflect.DeepEqual(got, []string{"1200", "1600"}) {
		t.Fatalf("times after remove = %v, want [1200 1600]", got)
	}

	// Final GET confirms the removal round-tripped through the DB, not just
	// reflected in the in-memory response of the DELETE call.
	resp, body = doRequest(t, http.MethodGet, "/api/machines/10.20.30.4/1/schedule", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("final get schedule: status = %d, body = %s", resp.StatusCode, body)
	}
	if got := getScheduleTimes(t, body); !reflect.DeepEqual(got, []string{"1200", "1600"}) {
		t.Fatalf("final times = %v, want [1200 1600]", got)
	}
}
