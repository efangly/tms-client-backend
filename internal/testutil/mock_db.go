// Package testutil provides shared test helpers for the TMS backend test suite.
package testutil

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"tms-backend/internal/database"
)

// SetupMockDB replaces database.DB with a sqlmock-backed GORM instance for the
// duration of the test. Returns the sqlmock controller for setting query
// expectations. The real DB is restored and the mock connection is closed
// automatically via t.Cleanup.
func SetupMockDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()

	sqlDB, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp),
		// MonitorPingsOption is intentionally omitted: GORM pings the connection
		// internally during Open(), and we do not want to track those pings.
		// Tests that need Ping() to fail should close sqlDB.Close() explicitly.
	)
	if err != nil {
		t.Fatalf("testutil.SetupMockDB: create sqlmock: %v", err)
	}

	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
		DisableWithReturning:      true,
	}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("testutil.SetupMockDB: open gorm: %v", err)
	}

	prev := database.DB
	database.DB = gormDB
	t.Cleanup(func() {
		database.DB = prev
		_ = sqlDB.Close()
	})
	return mock
}

// MachineColumns lists the database columns for master_machine in struct order.
var MachineColumns = []string{
	"machine_ip", "probe_no", "probe_all", "machine_name", "color",
	"chkOnline", "chkSms", "chkMail", "chkMon", "chkLine", "chkReport",
	"min_temp", "max_temp", "adj_temp", "sType",
}

// TempLogColumns lists the database columns for temp_log.
var TempLogColumns = []string{
	"machine_ip", "probe_no", "mcu_id", "temp_value", "real_value",
	"status", "send_time", "insert_time", "sDate", "sTime",
}

// TempLogArchiveColumns lists the database columns for temp_log_archive.
var TempLogArchiveColumns = TempLogColumns

// ArchiveManifestColumns lists the database columns for archive_manifest.
var ArchiveManifestColumns = []string{
	"id", "source_table", "period_date", "file_path", "row_count", "archived_at",
}

// TempErrorColumns lists the database columns for temp_error.
var TempErrorColumns = []string{
	"machine_ip", "probe_no", "machine_name", "temp_value", "error_time",
	"sms_status", "sms_send_time", "sms_send_status",
	"mail_status", "mail_send_time", "mail_send_status",
	"line_status", "line_send_time", "line_send_status",
	"min_temp", "max_temp", "mon_status",
	"mail_count", "sms_count", "line_count",
	"sType", "temp_status", "error_type",
}
