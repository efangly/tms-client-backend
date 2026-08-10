// Command script is a standalone, one-off tool for archiving a backup table
// that shares temp_log's schema but lives under a different table name (e.g.
// a table left over from a rename). It reuses ArchiveService.ArchiveFromTable
// so the data ends up in the same JSON-file + archive_manifest series the
// regular temp_log archiving already produces, and is picked up by the
// existing report/restore endpoints automatically.
//
// Usage (from the repo root, with DB_* and BACKUP_TABLE_NAME set via .env or
// the shell environment):
//
//	go run ./script
package main

import (
	"log"
	"os"

	"github.com/joho/godotenv"

	"tms-backend/internal/database"
	"tms-backend/internal/services"
)

func main() {
	_ = godotenv.Load()

	tableName := os.Getenv("BACKUP_TABLE_NAME")
	if tableName == "" {
		log.Fatal("BACKUP_TABLE_NAME env var is required (name of the backup table to archive)")
	}

	database.Connect()

	result, err := services.NewArchiveService().ArchiveFromTable(tableName, true)
	if err != nil {
		log.Fatalf("archive failed: %v", err)
	}
	log.Printf("Archived %d day(s), %d row(s) from %q into the temp_log archive series", result.DaysArchived, result.RowsArchived, tableName)
}
