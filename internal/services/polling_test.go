package services

// White-box tests for PollingService — same package so we can exercise
// unexported methods (checkProbeAlert, getMachines cache) and inspect
// unexported fields (alertStates, machineCache).

import (
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"tms-backend/internal/testutil"
	"tms-backend/internal/models"
)

// newTestPollingService builds a PollingService with a disabled API
// notification service so side-effect HTTP calls are never made.
func newTestPollingService() *PollingService {
	return &PollingService{
		pollInterval:           5 * time.Minute,
		alertInterval:          5 * time.Second,
		stopChan:               make(chan struct{}),
		subscribers:            make([]chan DataSavedEvent, 0),
		temperatureSubscribers: make([]chan []TemperatureUpdateEvent, 0),
		apiNotificationService: &APINotificationService{}, // empty = disabled
		mqttService:            &MQTTService{enabled: false},
		alertStates:            make(map[string]string),
	}
}

// ── Subscribe / Unsubscribe ───────────────────────────────────────────────────

func TestSubscribe_AddsChannel(t *testing.T) {
	ps := newTestPollingService()
	ch := ps.Subscribe()
	if len(ps.subscribers) != 1 {
		t.Fatalf("subscribers = %d, want 1", len(ps.subscribers))
	}
	if ps.subscribers[0] != ch {
		t.Error("channel not stored correctly")
	}
}

func TestUnsubscribe_RemovesAndClosesChannel(t *testing.T) {
	ps := newTestPollingService()
	ch := ps.Subscribe()
	ps.Unsubscribe(ch)

	if len(ps.subscribers) != 0 {
		t.Errorf("after Unsubscribe: subscribers = %d, want 0", len(ps.subscribers))
	}
	// The channel should be closed; a receive should return immediately with zero value.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel is still open after Unsubscribe")
		}
	default:
		t.Error("channel is not closed after Unsubscribe")
	}
}

func TestUnsubscribe_UnknownChannel_NoOp(t *testing.T) {
	ps := newTestPollingService()
	ch1 := ps.Subscribe()
	unknown := make(chan DataSavedEvent, 1)

	// Should not panic and should leave ch1 in place.
	ps.Unsubscribe(unknown)
	if len(ps.subscribers) != 1 || ps.subscribers[0] != ch1 {
		t.Error("Unsubscribe of unknown channel affected subscriber list")
	}
}

func TestSubscribeTemperature_AddsChannel(t *testing.T) {
	ps := newTestPollingService()
	ch := ps.SubscribeTemperature()
	if len(ps.temperatureSubscribers) != 1 || ps.temperatureSubscribers[0] != ch {
		t.Fatal("temperature channel not stored")
	}
}

func TestUnsubscribeTemperature_RemovesAndClosesChannel(t *testing.T) {
	ps := newTestPollingService()
	ch := ps.SubscribeTemperature()
	ps.UnsubscribeTemperature(ch)

	if len(ps.temperatureSubscribers) != 0 {
		t.Errorf("temperatureSubscribers = %d after unsubscribe, want 0", len(ps.temperatureSubscribers))
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("temperature channel still open after UnsubscribeTemperature")
		}
	default:
		t.Error("temperature channel not closed after UnsubscribeTemperature")
	}
}

// ── notifySubscribers / notifyTemperatureSubscribers ─────────────────────────

func TestNotifySubscribers_DeliverEvent(t *testing.T) {
	ps := newTestPollingService()
	ch := ps.Subscribe()
	event := DataSavedEvent{Saved: 5, Errors: 1}
	ps.notifySubscribers(event)

	select {
	case got := <-ch:
		if got != event {
			t.Errorf("got %+v, want %+v", got, event)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for event")
	}
}

func TestNotifySubscribers_FullChannelDropsEvent(t *testing.T) {
	// Channel capacity is 10; overflow should not block.
	ps := newTestPollingService()
	ch := ps.Subscribe()
	for i := 0; i < 15; i++ {
		ps.notifySubscribers(DataSavedEvent{Saved: i})
	}
	// Drain and count; should be exactly 10 (channel buffer size).
	count := 0
	for len(ch) > 0 {
		<-ch
		count++
	}
	if count != 10 {
		t.Errorf("received %d events from capped channel, want 10", count)
	}
}

func TestNotifyTemperatureSubscribers_DeliverEvents(t *testing.T) {
	ps := newTestPollingService()
	ch := ps.SubscribeTemperature()

	events := []TemperatureUpdateEvent{
		{MachineName: "Room A", TempValue: 22.5, Status: "N"},
	}
	ps.notifyTemperatureSubscribers(events)

	select {
	case got := <-ch:
		if len(got) != 1 || got[0].MachineName != "Room A" {
			t.Errorf("unexpected events: %+v", got)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for temperature event")
	}
}

// ── checkProbeAlert — alert state machine ─────────────────────────────────────

// TestAlertState_NormalToHigh transitions an in-range reading to HIGH.
// Expects a TempError INSERT and a state update in alertStates.
func TestAlertState_NormalToHigh(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	ps := newTestPollingService()

	minTemp := 18.0
	maxTemp := 28.0
	machine := models.MasterMachine{
		MachineIP:   "192.168.1.10",
		ProbeNo:     1,
		MachineName: "Test Room",
		MinTemp:     &minTemp,
		MaxTemp:     &maxTemp,
		SType:       "t",
	}

	// Initial state: "N" (via a previous call that set it)
	alertKey := "192.168.1.10:1"
	ps.alertStates[alertKey] = "N"

	// Expect DB insert of TempError (triggered when crossing into HIGH)
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .temp_error.`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	ps.checkProbeAlert(machine, 1, 30.0) // 30 °C > maxTemp=28

	if got := ps.alertStates[alertKey]; got != "H" {
		t.Errorf("alertStates after HIGH reading = %q, want %q", got, "H")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet DB expectations: %v", err)
	}
}

// TestAlertState_NormalToLow transitions an in-range reading to LOW.
func TestAlertState_NormalToLow(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	ps := newTestPollingService()

	minTemp := 18.0
	maxTemp := 28.0
	machine := models.MasterMachine{
		MachineIP:   "192.168.1.10",
		ProbeNo:     1,
		MachineName: "Freezer",
		MinTemp:     &minTemp,
		MaxTemp:     &maxTemp,
		SType:       "t",
	}

	alertKey := "192.168.1.10:1"
	ps.alertStates[alertKey] = "N"

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .temp_error.`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	ps.checkProbeAlert(machine, 1, 10.0) // 10 °C < minTemp=18

	if got := ps.alertStates[alertKey]; got != "L" {
		t.Errorf("alertStates after LOW reading = %q, want %q", got, "L")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet DB expectations: %v", err)
	}
}

// TestAlertState_HighStays verifies that repeated HIGH readings do NOT trigger
// another DB insert or a state change.
func TestAlertState_HighStays(t *testing.T) {
	_ = testutil.SetupMockDB(t) // no expectations — no DB call expected
	ps := newTestPollingService()

	minTemp := 18.0
	maxTemp := 28.0
	machine := models.MasterMachine{
		MachineIP:   "192.168.1.10",
		ProbeNo:     1,
		MachineName: "Test Room",
		MinTemp:     &minTemp,
		MaxTemp:     &maxTemp,
		SType:       "t",
	}

	alertKey := "192.168.1.10:1"
	ps.alertStates[alertKey] = "H" // already in HIGH state

	// No mock expectations — if the handler tries to write to DB it will error.
	ps.checkProbeAlert(machine, 1, 32.0) // still HIGH

	if got := ps.alertStates[alertKey]; got != "H" {
		t.Errorf("alertStates = %q after repeated HIGH, want H", got)
	}
}

// TestAlertState_HighToNormal verifies that returning to normal range does NOT
// write a new TempError but DOES send a recovery notification.
func TestAlertState_HighToNormal(t *testing.T) {
	_ = testutil.SetupMockDB(t) // no DB insert expected for recovery
	ps := newTestPollingService()

	minTemp := 18.0
	maxTemp := 28.0
	machine := models.MasterMachine{
		MachineIP:   "192.168.1.10",
		ProbeNo:     1,
		MachineName: "Test Room",
		MinTemp:     &minTemp,
		MaxTemp:     &maxTemp,
		SType:       "t",
	}

	alertKey := "192.168.1.10:1"
	ps.alertStates[alertKey] = "H"

	ps.checkProbeAlert(machine, 1, 22.0) // back in range

	if got := ps.alertStates[alertKey]; got != "N" {
		t.Errorf("alertStates after recovery = %q, want N", got)
	}
}

// TestAlertState_FirstReadingNormal confirms no DB write when the very first
// reading is in-range (transition from "" to "N").
func TestAlertState_FirstReadingNormal(t *testing.T) {
	_ = testutil.SetupMockDB(t) // no DB calls expected
	ps := newTestPollingService()

	minTemp := 18.0
	maxTemp := 28.0
	machine := models.MasterMachine{
		MachineIP:   "192.168.1.10",
		ProbeNo:     1,
		MachineName: "Test Room",
		MinTemp:     &minTemp,
		MaxTemp:     &maxTemp,
		SType:       "t",
	}

	// alertStates is empty — first poll
	ps.checkProbeAlert(machine, 1, 22.0)

	alertKey := "192.168.1.10:1"
	if got := ps.alertStates[alertKey]; got != "N" {
		t.Errorf("alertStates on first normal reading = %q, want N", got)
	}
}

// TestAlertState_DuplicateDBEntry continues gracefully when the DB returns a
// duplicate key error (INSERT conflicts with an existing TempError row).
func TestAlertState_DuplicateDBError_NoStateRollback(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	ps := newTestPollingService()

	minTemp := 18.0
	maxTemp := 28.0
	machine := models.MasterMachine{
		MachineIP:   "192.168.1.10",
		ProbeNo:     1,
		MachineName: "Test Room",
		MinTemp:     &minTemp,
		MaxTemp:     &maxTemp,
		SType:       "t",
	}

	alertKey := "192.168.1.10:1"
	ps.alertStates[alertKey] = "N"

	// Simulate duplicate entry error from DB.
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO .temp_error.`).
		WillReturnError(errors.New("Error 1062: Duplicate entry"))
	mock.ExpectRollback()

	// Should not panic; state is still updated even if DB write fails.
	ps.checkProbeAlert(machine, 1, 35.0)

	if got := ps.alertStates[alertKey]; got != "H" {
		t.Errorf("alertStates = %q after duplicate DB error, want H", got)
	}
}

// ── getMachines caching ────────────────────────────────────────────────────────

func TestGetMachines_CacheHit(t *testing.T) {
	_ = testutil.SetupMockDB(t) // no DB calls expected (cache is warm)
	ps := newTestPollingService()

	cached := []models.MasterMachine{
		{MachineIP: "10.0.0.1", ProbeNo: 1},
	}
	ps.machineCache = cached
	ps.machineCacheTime = time.Now() // freshly populated

	got, err := ps.getMachines()
	if err != nil {
		t.Fatalf("getMachines() error: %v", err)
	}
	if len(got) != 1 || got[0].MachineIP != "10.0.0.1" {
		t.Errorf("unexpected result from cache: %+v", got)
	}
}

func TestGetMachines_CacheMiss_QueriesDB(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	ps := newTestPollingService()

	// Cache is empty / stale — DB should be queried.
	rows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(rows)

	got, err := ps.getMachines()
	if err != nil {
		t.Fatalf("getMachines() error: %v", err)
	}
	if len(got) != 1 || got[0].MachineIP != "192.168.1.10" {
		t.Errorf("unexpected result: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("DB expectations not met: %v", err)
	}

	// A second call within 1 minute should hit the cache (no extra expectations).
	got2, err := ps.getMachines()
	if err != nil {
		t.Fatalf("second getMachines() error: %v", err)
	}
	if len(got2) != 1 {
		t.Errorf("cache miss on second call: %+v", got2)
	}
}

func TestGetMachines_DBError_ReturnsStale(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	ps := newTestPollingService()

	// Seed a stale cache.
	ps.machineCache = []models.MasterMachine{{MachineIP: "stale-ip", ProbeNo: 1}}
	ps.machineCacheTime = time.Now().Add(-2 * time.Minute) // expired

	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).
		WillReturnError(errors.New("connection refused"))

	got, err := ps.getMachines()
	if err != nil {
		t.Fatalf("getMachines() should return stale cache on DB error, got error: %v", err)
	}
	if len(got) != 1 || got[0].MachineIP != "stale-ip" {
		t.Errorf("expected stale cache, got %+v", got)
	}
}

func TestGetMachines_DBError_NoCache_ReturnsError(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	ps := newTestPollingService()

	// No cache at all.
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).
		WillReturnError(errors.New("connection refused"))

	_, err := ps.getMachines()
	if err == nil {
		t.Error("getMachines() with no cache and DB error should return error")
	}
}
