//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHealth(t *testing.T) {
	resp, body := doRequest(t, http.MethodGet, "/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, body)
	}

	var got struct {
		Status string `json:"status"`
		DB     bool   `json:"db"`
		MQTT   bool   `json:"mqtt"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.DB {
		t.Errorf("db = false, want true (real MySQL connection should be up)")
	}
	if got.MQTT {
		t.Errorf("mqtt = true, want false (MQTT_BROKER is unset in .env.e2e)")
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
}
