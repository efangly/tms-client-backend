package services

import (
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"tms-backend/internal/testutil"
)

func TestSerialService_Start_OpensOnlyComPrefixedMachines(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	rows := sqlmock.NewRows(testutil.MachineColumns).
		AddRow("192.168.1.10", 1, 4, "Room A", "FF5733", "1", "0", "1", "0", "0", "1", 18.0, 28.0, 0.0, "t").
		AddRow("COM3", 1, 1, "Freezer 1", "000000", "1", "0", "1", "0", "0", "1", -20.0, -10.0, 0.0, "t").
		// Same COM port, second probe on the same physical device — must
		// not open a second connection for it.
		AddRow("COM3", 2, 1, "Freezer 1", "000000", "1", "0", "1", "0", "0", "1", -20.0, -10.0, 0.0, "h").
		AddRow("com5", 1, 1, "Freezer 2", "000000", "1", "0", "1", "0", "0", "1", -20.0, -10.0, 0.0, "t")
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(rows)

	s := NewSerialService()
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("DB expectations not met: %v", err)
	}

	if _, ok := s.GetConnection("192.168.1.10"); ok {
		t.Error("expected no connection tracked for a TCP-style machine_ip")
	}
	if _, ok := s.GetConnection("COM3"); !ok {
		t.Error("expected a connection tracked for COM3")
	}
	if _, ok := s.GetConnection("com5"); !ok {
		t.Error("expected a connection tracked for com5 (case-insensitive COM prefix)")
	}

	s.Stop()
}

func TestSerialService_Start_NoMachinesIsNotAnError(t *testing.T) {
	mock := testutil.SetupMockDB(t)

	rows := sqlmock.NewRows(testutil.MachineColumns)
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnRows(rows)

	s := NewSerialService()
	if err := s.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if _, ok := s.GetConnection("COM3"); ok {
		t.Error("expected no connections when no machines are configured")
	}
}

func TestSerialService_Start_DBErrorIsReturned(t *testing.T) {
	mock := testutil.SetupMockDB(t)
	mock.ExpectQuery(`SELECT \* FROM .master_machine.`).WillReturnError(errors.New("simulated DB failure"))

	s := NewSerialService()
	if err := s.Start(); err == nil {
		t.Fatal("expected an error when the DB query fails")
	}
}
