// Package handlers_test contains integration tests for all HTTP handlers.
//
// Each test spins up a minimal Fiber app (no system tray, no TCP polling),
// replaces database.DB with a sqlmock-backed GORM instance, and exercises the
// full HTTP → handler → GORM stack using Fiber's in-process app.Test helper.
package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/gofiber/fiber/v2"

	"tms-backend/internal/database"
	"tms-backend/internal/handlers"
	"tms-backend/internal/models"
	"tms-backend/internal/services"
	"tms-backend/internal/testutil"
)

// ── test app factory ──────────────────────────────────────────────────────────

// newTestApp constructs a Fiber app using the same route table as main.go
// (via handlers.RegisterRoutes), without the system tray, MQTT, or TCP
// polling dependencies.
func newTestApp() *fiber.App {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	handlers.RegisterRoutes(app)
	return app
}

// mustTest calls app.Test and fatals on transport error (not HTTP errors).
func mustTest(t *testing.T, app *fiber.App, req *http.Request) *http.Response {
	t.Helper()
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func bodyString(t *testing.T, r io.Reader) string {
	t.Helper()
	b, _ := io.ReadAll(r)
	return string(b)
}

// ── /health ───────────────────────────────────────────────────────────────────

func TestHealth_DBUp(t *testing.T) {
	testutil.SetupMockDB(t) // Ping() returns nil by default with sqlmock
	// Ensure no MQTT service is registered so mqtt=false.
	services.GlobalMQTTService = nil

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp := mustTest(t, newTestApp(), req)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("status field = %v, want ok", body["status"])
	}
	if body["db"] != true {
		t.Errorf("db field = %v, want true", body["db"])
	}
}

func TestHealth_DBDown(t *testing.T) {
	testutil.SetupMockDB(t)
	// Close the underlying connection — Ping() will then return an error,
	// making dbOK=false and the handler return 503.
	sqlDB, _ := database.DB.DB()
	sqlDB.Close()
	services.GlobalMQTTService = nil

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp := mustTest(t, newTestApp(), req)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "degraded" {
		t.Errorf("status field = %v, want degraded", body["status"])
	}
}

// ── GET /api/devices ──────────────────────────────────────────────────────────

func TestGetDevices_ReturnsAll(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	rows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t").
		AddRow("192.168.1.11", 1, 1, "Room B", "00FF00", "0", "0", "0", "0", "0", "0", 10.0, 25.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(rows)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/devices", nil))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var machines []models.MasterMachine
	json.NewDecoder(resp.Body).Decode(&machines)
	if len(machines) != 2 {
		t.Errorf("len(machines) = %d, want 2", len(machines))
	}
	if machines[0].MachineIP != "192.168.1.10" {
		t.Errorf("machines[0].MachineIP = %q", machines[0].MachineIP)
	}
}

func TestGetDevices_DBError_Returns500(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnError(errDB)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/devices", nil))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// ── GET /api/devices/:id ──────────────────────────────────────────────────────

func TestGetDevice_Found(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	rows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine. WHERE machine_ip = .+ AND probe_no`).
		WillReturnRows(rows)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/devices/192.168.1.10?probeNo=1", nil))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var m models.MasterMachine
	json.NewDecoder(resp.Body).Decode(&m)
	if m.MachineIP != "192.168.1.10" {
		t.Errorf("MachineIP = %q", m.MachineIP)
	}
}

func TestGetDevice_NotFound(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .master_machine. WHERE machine_ip`).
		WillReturnRows(sqlmock.NewRows(testutil.MachineColumns)) // empty

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/devices/10.0.0.1?probeNo=1", nil))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ── POST /api/devices ─────────────────────────────────────────────────────────

func TestCreateDevice_Success(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .master_machine.`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	body := `{
		"machineIp":"192.168.1.20","probeNo":1,"machineName":"Lab",
		"minTemp":15,"maxTemp":30,"sType":"t"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201 — body: %s", resp.StatusCode, bodyString(t, resp.Body))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("DB expectations: %v", err)
	}
}

func TestCreateDevice_DefaultsSType(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .master_machine.`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// No sType in body → handler should default to "t"
	body := `{"machineIp":"192.168.1.30","machineName":"Defaults"}`
	req := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d — %s", resp.StatusCode, bodyString(t, resp.Body))
	}

	var m models.MasterMachine
	json.NewDecoder(resp.Body).Decode(&m)
	if m.SType != "t" {
		t.Errorf("SType = %q, want t (default)", m.SType)
	}
	if m.ProbeNo != 1 {
		t.Errorf("ProbeNo = %d, want 1 (default)", m.ProbeNo)
	}
}

func TestCreateDevice_BadJSON_Returns400(t *testing.T) {
	testutil.SetupMockDB(t) // no expectations
	req := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateDevice_DBError_Returns500(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .master_machine.`).WillReturnError(errDB)
	mock.ExpectRollback()

	body := `{"machineIp":"192.168.1.50","machineName":"Fail","sType":"t"}`
	req := httptest.NewRequest(http.MethodPost, "/api/devices", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// ── PUT /api/devices/:id ──────────────────────────────────────────────────────

func TestUpdateDevice_Success(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	// 1. First() to find existing record
	foundRow := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(foundRow)

	// 2. Updates() → BEGIN + UPDATE + COMMIT
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .master_machine.`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// 3. Re-fetch after update — GORM wraps the condition in parens on re-fetch
	updatedRow := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Updated Name", "FF5733", "1", "0", "1", "0", "0", "1", 15.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(updatedRow)

	body := `{"machineName":"Updated Name","minTemp":15.0}`
	req := httptest.NewRequest(http.MethodPut, "/api/devices/192.168.1.10?probeNo=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d — %s", resp.StatusCode, bodyString(t, resp.Body))
	}
	var m models.MasterMachine
	json.NewDecoder(resp.Body).Decode(&m)
	if m.MachineName != "Updated Name" {
		t.Errorf("MachineName = %q, want Updated Name", m.MachineName)
	}
}

// TestUpdateDevice_JSONKeysTranslated is the regression test for the bug where
// updating fields like "machineName" or "minTemp" (JSON keys) returned 500
// because GORM used those as literal SQL column names instead of "machine_name"
// and "min_temp".
func TestUpdateDevice_JSONKeysTranslated(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	foundRow := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Old Name", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(foundRow)

	// The UPDATE must reference DB column names (machine_name, min_temp, max_temp, adj_temp)
	// NOT the JSON keys (machineName, minTemp, maxTemp, adjTemp).
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .master_machine. SET .*(machine_name|min_temp|max_temp)`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	refetchRow := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "New Name", "FF5733", "1", "0", "1", "0", "0", "1", 15.0, 30.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(refetchRow)

	// Send all the camelCase JSON keys that previously caused "Unknown column" errors.
	body := `{"machineName":"New Name","minTemp":15.0,"maxTemp":30.0,"adjTemp":0.5,"probeAll":2}`
	req := httptest.NewRequest(http.MethodPut, "/api/devices/192.168.1.10?probeNo=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (want 200) — JSON key translation is broken: %s",
			resp.StatusCode, bodyString(t, resp.Body))
	}
}

func TestUpdateDevice_NotFound(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .master_machine. WHERE machine_ip`).
		WillReturnRows(sqlmock.NewRows(testutil.MachineColumns))

	body := `{"machineName":"x"}`
	req := httptest.NewRequest(http.MethodPut, "/api/devices/10.0.0.99?probeNo=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUpdateDevice_PrimaryKeyFieldsStripped(t *testing.T) {
	// Even if the client sends machineIp/probeNo, they must be silently ignored.
	mock := testutil.SetupMockDB(t)

	foundRow := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(foundRow)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .master_machine.`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	refetchRow := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(refetchRow)

	// Try to mutate the primary key — handler must strip these.
	body := `{"machineIp":"9.9.9.9","probeNo":99,"machineName":"safe"}`
	req := httptest.NewRequest(http.MethodPut, "/api/devices/192.168.1.10?probeNo=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d — %s", resp.StatusCode, bodyString(t, resp.Body))
	}
}

// ── DELETE /api/devices/:id ───────────────────────────────────────────────────

func TestDeleteDevice_ByProbe_Success(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM .master_machine. WHERE machine_ip = .+ AND probe_no`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	req := httptest.NewRequest(http.MethodDelete, "/api/devices/192.168.1.10?probeNo=1", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["success"] != true {
		t.Errorf("body.success = %v, want true", body["success"])
	}
}

func TestDeleteDevice_AllProbes_WhenNoProbeNo(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	// Without probeNo query param, the handler deletes ALL probes for that IP.
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM .master_machine. WHERE machine_ip`).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	req := httptest.NewRequest(http.MethodDelete, "/api/devices/192.168.1.10", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// ── GET /api/machines ─────────────────────────────────────────────────────────

func TestGetMachines_ReturnsWithStatus(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	machineRows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(machineRows)

	// Raw SQL that fetches latest temp per machine
	latestRows := sqlmock.NewRows([]string{"machine_ip", "probe_no", "temp_value", "insert_time"}).
		AddRow("192.168.1.10", 1, 22.5, time.Now())
	mock.ExpectQuery(`SELECT t1.machine_ip, t1.probe_no, t1.temp_value, t1.insert_time`).
		WillReturnRows(latestRows)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/machines", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d — %s", resp.StatusCode, bodyString(t, resp.Body))
	}

	var result []models.MachineWithStatus
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result[0].OnlineStatus != "Online" {
		t.Errorf("OnlineStatus = %q, want Online (recent reading)", result[0].OnlineStatus)
	}
	if result[0].CurrentValue == nil || *result[0].CurrentValue != 22.5 {
		t.Errorf("CurrentValue = %v, want 22.5", result[0].CurrentValue)
	}
}

func TestGetMachines_OfflineWhenOldReading(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	machineRows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(machineRows)

	// Last reading was >10 minutes ago → Offline
	oldTime := time.Now().Add(-15 * time.Minute)
	latestRows := sqlmock.NewRows([]string{"machine_ip", "probe_no", "temp_value", "insert_time"}).
		AddRow("192.168.1.10", 1, 22.5, oldTime)
	mock.ExpectQuery(`SELECT t1.machine_ip, t1.probe_no, t1.temp_value, t1.insert_time`).
		WillReturnRows(latestRows)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/machines", nil))
	var result []models.MachineWithStatus
	json.NewDecoder(resp.Body).Decode(&result)
	if result[0].OnlineStatus != "Offline" {
		t.Errorf("OnlineStatus = %q, want Offline", result[0].OnlineStatus)
	}
}

// ── PUT /api/machines/:machineIp/:probeNo ─────────────────────────────────────

func TestUpdateMachine_Success(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	foundRow := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(foundRow)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE .master_machine.`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	refetchRow := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "New Name", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(refetchRow)

	body := `{"machineName":"New Name"}`
	req := httptest.NewRequest(http.MethodPut, "/api/machines/192.168.1.10/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d — %s", resp.StatusCode, bodyString(t, resp.Body))
	}
}

func TestUpdateMachine_NotFound(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .master_machine. WHERE machine_ip`).
		WillReturnRows(sqlmock.NewRows(testutil.MachineColumns))

	body := `{"machineName":"x"}`
	req := httptest.NewRequest(http.MethodPut, "/api/machines/10.0.0.99/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// ── GET /api/temp-logs ────────────────────────────────────────────────────────

func TestGetTempLogs_NoFilter_ReturnsAll(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	insertTime := time.Now()
	tempVal := 22.5
	realVal := 2250
	status := "N"

	rows := sqlmock.NewRows(testutil.TempLogColumns).
		AddRow("192.168.1.10", 1, "Room A", tempVal, realVal, status, insertTime, insertTime, "20240115", "14")
	mock.ExpectQuery(`SELECT \* FROM .temp_log. ORDER BY insert_time DESC LIMIT`).
		WillReturnRows(rows)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/temp-logs", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d — %s", resp.StatusCode, bodyString(t, resp.Body))
	}

	var logs []models.TempLog
	json.NewDecoder(resp.Body).Decode(&logs)
	if len(logs) != 1 {
		t.Errorf("len(logs) = %d, want 1", len(logs))
	}
}

func TestGetTempLogs_WithDateFilter(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	rows := sqlmock.NewRows(testutil.TempLogColumns)
	mock.ExpectQuery(`SELECT \* FROM .temp_log. WHERE insert_time BETWEEN`).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/api/temp-logs?startDate=2024-01-01&endDate=2024-01-31", nil)
	resp := mustTest(t, newTestApp(), req)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("DB expectations: %v", err)
	}
}

func TestGetTempLogs_CustomLimit(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	rows := sqlmock.NewRows(testutil.TempLogColumns)
	mock.ExpectQuery(`SELECT \* FROM .temp_log. ORDER BY insert_time DESC LIMIT`).
		WillReturnRows(rows)

	req := httptest.NewRequest(http.MethodGet, "/api/temp-logs?limit=5", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestGetTempLogs_DeviceFilter(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).
		WillReturnRows(sqlmock.NewRows(testutil.MachineColumns))
	mock.ExpectQuery(`SELECT \* FROM .temp_log.`).
		WillReturnRows(sqlmock.NewRows(testutil.TempLogColumns))

	req := httptest.NewRequest(http.MethodGet, "/api/temp-logs?devices=192.168.1.10,192.168.1.11", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body: %s", resp.StatusCode, bodyString(t, resp.Body))
	}
}

func TestGetTempLogs_DeviceProbeFilter(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	machineRows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 2, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t").
		AddRow("192.168.1.10", 2, 2, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "h")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(machineRows)
	mock.ExpectQuery(`SELECT \* FROM .temp_log.`).
		WillReturnRows(sqlmock.NewRows(testutil.TempLogColumns))

	// Only probe 1 of 192.168.1.10 should be requested — probe 2 filtered out.
	req := httptest.NewRequest(http.MethodGet, "/api/temp-logs?devices=192.168.1.10:1", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body: %s", resp.StatusCode, bodyString(t, resp.Body))
	}
}

// ── GET /api/reports/templog ──────────────────────────────────────────────────

func TestGetTempLogReport_MissingDates_Returns400(t *testing.T) {
	testutil.SetupMockDB(t) // no DB calls expected

	cases := []string{
		"/api/reports/templog",
		"/api/reports/templog?startDate=2024-01-01",
		"/api/reports/templog?endDate=2024-01-31",
	}
	for _, url := range cases {
		resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, url, nil))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("url=%q: status = %d, want 400", url, resp.StatusCode)
		}
	}
}

func TestGetTempLogReport_InvalidDateFormat_Returns400(t *testing.T) {
	testutil.SetupMockDB(t)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/templog?startDate=01-01-2024&endDate=31-01-2024", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid date format", resp.StatusCode)
	}
}

func TestGetTempLogReport_Success(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	insertTime := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)
	tempVal := 22.5
	realVal := 2250
	status := "N"

	logRows := sqlmock.NewRows(testutil.TempLogColumns).
		AddRow("192.168.1.10", 1, "Room A", tempVal, realVal, status, insertTime, insertTime, "20240115", "14")
	mock.ExpectQuery(`SELECT \* FROM .temp_log. WHERE insert_time BETWEEN`).
		WillReturnRows(logRows)

	machineRows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(machineRows)

	req := httptest.NewRequest(http.MethodGet, "/api/reports/templog?startDate=2024-01-01&endDate=2024-01-31", nil)
	resp := mustTest(t, newTestApp(), req)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d — %s", resp.StatusCode, bodyString(t, resp.Body))
	}

	var body map[string]json.RawMessage
	json.NewDecoder(resp.Body).Decode(&body)
	if _, ok := body["data"]; !ok {
		t.Error("response missing 'data' key")
	}
	if _, ok := body["series"]; !ok {
		t.Error("response missing 'series' key")
	}
}

func TestGetTempLogReport_DeviceFilter(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	// GORM wraps the WHERE in parens when combining conditions, so use a loose pattern.
	mock.ExpectQuery(`SELECT \* FROM .temp_log.`).
		WillReturnRows(sqlmock.NewRows(testutil.TempLogColumns))
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).
		WillReturnRows(sqlmock.NewRows(testutil.MachineColumns))

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/reports/templog?startDate=2024-01-01&endDate=2024-01-31&devices=192.168.1.10,192.168.1.11",
		nil,
	)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body: %s", resp.StatusCode, bodyString(t, resp.Body))
	}
}

func TestGetTempLogReport_DeviceProbeFilter(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	// GORM wraps the WHERE in parens when combining conditions, so use a loose pattern.
	mock.ExpectQuery(`SELECT \* FROM .temp_log.`).
		WillReturnRows(sqlmock.NewRows(testutil.TempLogColumns))
	machineRows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 2, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t").
		AddRow("192.168.1.10", 2, 2, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "h").
		AddRow("192.168.1.11", 1, 1, "Room B", "00FF00", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(machineRows)

	// Probe 1 only for 192.168.1.10 (mixed with a bare device for 192.168.1.11 — all its probes).
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/reports/templog?startDate=2024-01-01&endDate=2024-01-31&devices=192.168.1.10:1,192.168.1.11",
		nil,
	)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 — body: %s", resp.StatusCode, bodyString(t, resp.Body))
	}
}

// ── GET /api/temp-errors ──────────────────────────────────────────────────────

func TestGetTempErrors_NoFilter(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	errorTime := time.Now().Add(-1 * time.Hour)
	machineName := "Room A"
	tempVal := 35.0
	minTemp := 18.0
	maxTemp := 28.0

	rows := sqlmock.NewRows(testutil.TempErrorColumns).
		AddRow("192.168.1.10", 1, machineName, tempVal, errorTime,
			0, nil, 0,
			0, nil, 0,
			0, nil, 0,
			minTemp, maxTemp, 0,
			0, 0, 0,
			"t", "p", "o")
	mock.ExpectQuery(`SELECT \* FROM .temp_error. ORDER BY error_time DESC LIMIT`).
		WillReturnRows(rows)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/temp-errors", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d — %s", resp.StatusCode, bodyString(t, resp.Body))
	}

	var errs []models.TempError
	json.NewDecoder(resp.Body).Decode(&errs)
	if len(errs) != 1 {
		t.Errorf("len(errs) = %d, want 1", len(errs))
	}
	if errs[0].MachineIP != "192.168.1.10" {
		t.Errorf("MachineIP = %q", errs[0].MachineIP)
	}
}

func TestGetTempErrors_WithDateFilter(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .temp_error. WHERE error_time BETWEEN`).
		WillReturnRows(sqlmock.NewRows(testutil.TempErrorColumns))

	req := httptest.NewRequest(http.MethodGet, "/api/temp-errors?startDate=2024-01-01&endDate=2024-01-31", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// ── POST /api/poll ────────────────────────────────────────────────────────────

func TestTriggerPoll_ServiceNil_Returns503(t *testing.T) {
	testutil.SetupMockDB(t)
	prev := services.GlobalPollingService
	services.GlobalPollingService = nil
	t.Cleanup(func() { services.GlobalPollingService = prev })

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodPost, "/api/poll", nil))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestTriggerPoll_WithService_Returns200(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	// The background goroutine TriggerOnce() spawns calls pollAndSave(),
	// which SELECTs machines. Return empty rows so the goroutine exits early
	// without attempting TCP connections.
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).
		WillReturnRows(sqlmock.NewRows(testutil.MachineColumns))

	prev := services.GlobalPollingService
	services.GlobalPollingService = services.NewPollingService()
	t.Cleanup(func() { services.GlobalPollingService = prev })

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodPost, "/api/poll", nil))
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "polling started" {
		t.Errorf("body.status = %v", body["status"])
	}
	// Wait for the background goroutine to finish so its DB call doesn't
	// leak into the next test's mock.
	time.Sleep(200 * time.Millisecond)
}

// ── response shape helpers ────────────────────────────────────────────────────

// TestDeviceResponseShape validates that the JSON keys in the device response
// match the documented API contract (camelCase field names).
func TestDeviceResponseShape(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	rows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 2, 4, "Room A", "FFFFFF", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.5, "h")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(rows)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/devices", nil))

	var raw []map[string]any
	json.NewDecoder(resp.Body).Decode(&raw)
	if len(raw) != 1 {
		t.Fatalf("expected 1 device, got %d", len(raw))
	}
	d := raw[0]

	requiredKeys := []string{
		"machineIp", "probeNo", "probeAll", "machineName", "color",
		"chkOnline", "chkSms", "chkMail", "chkMon", "chkLine", "chkReport",
		"minTemp", "maxTemp", "adjTemp", "sType",
	}
	for _, k := range requiredKeys {
		if _, ok := d[k]; !ok {
			t.Errorf("response missing key %q", k)
		}
	}
	// MasterUser.Password must not appear — it has json:"-"
	if _, ok := d["password"]; ok {
		t.Error("response must not include 'password'")
	}
}

// TestMachineWithStatusShape validates the /api/machines response shape.
func TestMachineWithStatusShape(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	machineRows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(machineRows)

	latestRows := sqlmock.NewRows([]string{"machine_ip", "probe_no", "temp_value", "insert_time"}).
		AddRow("192.168.1.10", 1, 22.5, time.Now())
	mock.ExpectQuery(`SELECT t1.machine_ip`).WillReturnRows(latestRows)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/machines", nil))
	var raw []map[string]any
	json.NewDecoder(resp.Body).Decode(&raw)
	if len(raw) != 1 {
		t.Fatalf("expected 1 machine, got %d", len(raw))
	}
	for _, k := range []string{"currentValue", "lastUpdate", "onlineStatus"} {
		if _, ok := raw[0][k]; !ok {
			t.Errorf("/api/machines response missing key %q", k)
		}
	}
}

// ── POST /api/archive/run ─────────────────────────────────────────────────────

func TestRunArchiveHandler_ServiceNil_Returns503(t *testing.T) {
	testutil.SetupMockDB(t)
	prev := services.GlobalArchiveService
	services.GlobalArchiveService = nil
	t.Cleanup(func() { services.GlobalArchiveService = prev })

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodPost, "/api/archive/run", nil))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestRunArchiveHandler_NoArchivableDays_Returns200(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	// RunArchive's day-listing query returns no rows -> nothing further happens.
	mock.ExpectQuery(`SELECT DISTINCT DATE_FORMAT`).
		WillReturnRows(sqlmock.NewRows([]string{"day"}))

	prev := services.GlobalArchiveService
	services.GlobalArchiveService = services.NewArchiveService()
	t.Cleanup(func() { services.GlobalArchiveService = prev })

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodPost, "/api/archive/run", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — %s", resp.StatusCode, bodyString(t, resp.Body))
	}

	var result struct {
		DaysArchived int `json:"daysArchived"`
		RowsArchived int `json:"rowsArchived"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.DaysArchived != 0 || result.RowsArchived != 0 {
		t.Errorf("result = %+v, want zero", result)
	}
}

// ── GET /api/archive ──────────────────────────────────────────────────────────

func TestGetArchiveManifests_ReturnsList(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	rows := sqlmock.NewRows(testutil.ArchiveManifestColumns).
		AddRow(1, "temp_log", "2026-01-10", "archives/temp_log/2026/01/temp_log_2026-01-10.json", 42, time.Now())
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).WillReturnRows(rows)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/archive", nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — %s", resp.StatusCode, bodyString(t, resp.Body))
	}

	var manifests []models.ArchiveManifest
	json.NewDecoder(resp.Body).Decode(&manifests)
	if len(manifests) != 1 || manifests[0].PeriodDate != "2026-01-10" {
		t.Errorf("manifests = %+v", manifests)
	}
}

func TestGetArchiveManifests_DBError_Returns500(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).WillReturnError(errDB)

	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodGet, "/api/archive", nil))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// ── POST /api/archive/restore ─────────────────────────────────────────────────

func TestRestoreArchiveHandler_MissingDates_Returns400(t *testing.T) {
	testutil.SetupMockDB(t)
	resp := mustTest(t, newTestApp(), httptest.NewRequest(http.MethodPost, "/api/archive/restore", nil))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRestoreArchiveHandler_InvalidDateFormat_Returns400(t *testing.T) {
	testutil.SetupMockDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/archive/restore?startDate=10-01-2026&endDate=2026-01-10", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRestoreArchiveHandler_ServiceNil_Returns503(t *testing.T) {
	testutil.SetupMockDB(t)
	prev := services.GlobalArchiveService
	services.GlobalArchiveService = nil
	t.Cleanup(func() { services.GlobalArchiveService = prev })

	req := httptest.NewRequest(http.MethodPost, "/api/archive/restore?startDate=2026-01-01&endDate=2026-01-10", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestRestoreArchiveHandler_NoManifests_Returns200(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .archive_manifest.`).
		WillReturnRows(sqlmock.NewRows(testutil.ArchiveManifestColumns))

	prev := services.GlobalArchiveService
	services.GlobalArchiveService = services.NewArchiveService()
	t.Cleanup(func() { services.GlobalArchiveService = prev })

	req := httptest.NewRequest(http.MethodPost, "/api/archive/restore?startDate=2026-01-01&endDate=2026-01-10", nil)
	resp := mustTest(t, newTestApp(), req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — %s", resp.StatusCode, bodyString(t, resp.Body))
	}

	var result struct {
		DaysRestored int `json:"daysRestored"`
		RowsRestored int `json:"rowsRestored"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.DaysRestored != 0 || result.RowsRestored != 0 {
		t.Errorf("result = %+v, want zero", result)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// errDB is a sentinel error that represents a generic database failure.
var errDB = errorString("simulated database error")

type errorString string

func (e errorString) Error() string { return string(e) }

// Ensure errorString implements the error interface.
var _ error = errorString("")

// Verify we're not accidentally importing bytes or io unused.
var _ = bytes.NewReader
var _ = io.ReadAll
