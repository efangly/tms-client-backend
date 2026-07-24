package services

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/utils"
)

// GlobalArchiveService is the process-wide archive service instance, wired up
// in main.go alongside GlobalPollingService.
var GlobalArchiveService *ArchiveService

var archiveDir = "./archives"
var archiveRetentionDays = 30
var archiveRunHour = 2

func init() {
	if dir := os.Getenv("ARCHIVE_DIR"); dir != "" {
		archiveDir = dir
	}
	if days := os.Getenv("ARCHIVE_RETENTION_DAYS"); days != "" {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			archiveRetentionDays = n
		}
	}
	if hour := os.Getenv("ARCHIVE_RUN_HOUR"); hour != "" {
		if n, err := strconv.Atoi(hour); err == nil && n >= 0 && n < 24 {
			archiveRunHour = n
		}
	}
}

// ArchiveDir returns the directory archive files are written to. Exposed so
// callers (e.g. the e2e suite) that need to know the real value don't have to
// re-read ARCHIVE_DIR themselves — env vars are only applied here at package
// init time, which runs before any in-process .env loading in main()/TestMain.
func ArchiveDir() string {
	return archiveDir
}

// ArchiveResult summarizes the outcome of a RunArchive call.
type ArchiveResult struct {
	DaysArchived int `json:"daysArchived"`
	RowsArchived int `json:"rowsArchived"`
}

// RestoreResult summarizes the outcome of a RestoreRange call.
type RestoreResult struct {
	DaysRestored int `json:"daysRestored"`
	RowsRestored int `json:"rowsRestored"`
}

// ArchiveService periodically moves temp_log rows older than the configured
// retention window out of the database and into local JSON files, following
// the same ticker + stopChan + WaitGroup pattern as PollingService.
type ArchiveService struct {
	stopChan    chan struct{}
	wg          sync.WaitGroup
	running     bool
	mu          sync.Mutex
	lastRunDate string
}

func NewArchiveService() *ArchiveService {
	return &ArchiveService{
		stopChan: make(chan struct{}),
	}
}

func (a *ArchiveService) Start() {
	defer func() {
		if r := recover(); r != nil {
			utils.LogError("PANIC in archive service: %v", r)
			log.Printf("PANIC in archive service: %v", r)
			a.mu.Lock()
			a.running = false
			a.mu.Unlock()
		}
	}()

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return
	}
	a.running = true
	a.stopChan = make(chan struct{})
	stopChan := a.stopChan
	a.mu.Unlock()

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							utils.LogError("PANIC in archive ticker iteration: %v", r)
							log.Printf("PANIC in archive ticker iteration: %v (will retry next tick)", r)
						}
					}()
					a.runIfDue()
				}()
			case <-stopChan:
				return
			}
		}
	}()
}

func (a *ArchiveService) Stop() {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	a.running = false
	stopChan := a.stopChan
	a.mu.Unlock()

	close(stopChan)

	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("Archive service stopped gracefully")
	case <-time.After(10 * time.Second):
		log.Println("Archive service stop timed out after 10s, forcing shutdown")
	}
}

// runIfDue triggers RunArchive once per calendar day, at or after archiveRunHour.
func (a *ArchiveService) runIfDue() {
	now := database.GetThailandTime()
	if now.Hour() < archiveRunHour {
		return
	}
	today := now.Format("2006-01-02")

	a.mu.Lock()
	if a.lastRunDate == today {
		a.mu.Unlock()
		return
	}
	a.lastRunDate = today
	a.mu.Unlock()

	if _, err := a.RunArchive(); err != nil {
		utils.LogError("Scheduled archive run failed: %v", err)
	}
}

// RunArchive archives every day of temp_log data older than the retention
// window that hasn't already been archived. Callable from the ticker or a
// manual API trigger.
func (a *ArchiveService) RunArchive() (ArchiveResult, error) {
	result := ArchiveResult{}
	cutoff := database.GetThailandTime().AddDate(0, 0, -archiveRetentionDays)

	// DATE_FORMAT (not DATE()) so the driver scans a plain string, not a
	// time.Time — the DSN has parseTime=True which affects DATE-typed columns.
	var days []string
	if err := database.DB.Raw(
		`SELECT DISTINCT DATE_FORMAT(insert_time, '%Y-%m-%d') AS day FROM temp_log WHERE insert_time < ? ORDER BY day`,
		cutoff.Format("2006-01-02 00:00:00"),
	).Scan(&days).Error; err != nil {
		return result, fmt.Errorf("failed to list archivable days: %w", err)
	}

	for _, day := range days {
		var existing models.ArchiveManifest
		err := database.DB.Where("source_table = ? AND period_date = ?", "temp_log", day).First(&existing).Error
		if err == nil {
			continue // already archived
		}

		rows, err := a.archiveDay(day)
		if err != nil {
			utils.LogError("Failed to archive temp_log for %s: %v", day, err)
			continue
		}
		if rows > 0 {
			result.DaysArchived++
			result.RowsArchived += rows
		}
	}

	return result, nil
}

// archiveDay archives all temp_log rows for a single day (YYYY-MM-DD):
// write them to a JSON file, verify the write, then delete the rows from
// temp_log and record the archive_manifest entry in one transaction.
func (a *ArchiveService) archiveDay(day string) (int, error) {
	var rows []models.TempLog
	if err := database.DB.Where("insert_time BETWEEN ? AND ?", day+" 00:00:00", day+" 23:59:59").
		Order("insert_time ASC").Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("query rows: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	filePath, err := writeArchiveFile("temp_log", day, rows)
	if err != nil {
		return 0, fmt.Errorf("write archive file: %w", err)
	}

	if err := verifyArchiveFile(filePath, len(rows)); err != nil {
		return 0, fmt.Errorf("verify archive file: %w", err)
	}

	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("insert_time BETWEEN ? AND ?", day+" 00:00:00", day+" 23:59:59").
			Delete(&models.TempLog{}).Error; err != nil {
			return err
		}
		manifest := models.ArchiveManifest{
			SourceTable: "temp_log",
			PeriodDate:  day,
			FilePath:    filePath,
			RowCount:    len(rows),
			ArchivedAt:  database.GetThailandTime(),
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "source_table"}, {Name: "period_date"}},
			DoUpdates: clause.AssignmentColumns([]string{"file_path", "row_count", "archived_at"}),
		}).Create(&manifest).Error
	}); err != nil {
		return 0, fmt.Errorf("delete+record manifest: %w", err)
	}

	return len(rows), nil
}

// RestoreRange loads archived temp_log data for [startDate, endDate] (each
// YYYY-MM-DD) from local files into the temp_log_archive table, so report
// endpoints can read it without touching the live temp_log table.
func (a *ArchiveService) RestoreRange(startDate, endDate string) (RestoreResult, error) {
	result := RestoreResult{}

	var manifests []models.ArchiveManifest
	if err := database.DB.Where("source_table = ? AND period_date BETWEEN ? AND ?", "temp_log", startDate, endDate).
		Find(&manifests).Error; err != nil {
		return result, fmt.Errorf("failed to list manifests: %w", err)
	}

	for _, m := range manifests {
		data, err := os.ReadFile(m.FilePath)
		if err != nil {
			log.Printf("archive restore: skipping missing file %s: %v", m.FilePath, err)
			continue
		}

		var rows []models.TempLogArchive
		if err := json.Unmarshal(data, &rows); err != nil {
			log.Printf("archive restore: skipping unreadable file %s: %v", m.FilePath, err)
			continue
		}
		if len(rows) == 0 {
			continue
		}

		if err := database.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
			log.Printf("archive restore: failed to insert rows for %s: %v", m.FilePath, err)
			continue
		}

		result.DaysRestored++
		result.RowsRestored += len(rows)
	}

	return result, nil
}

// writeArchiveFile writes rows as a JSON array to
// {archiveDir}/{sourceTable}/{YYYY}/{MM}/{sourceTable}_{day}.json.
func writeArchiveFile(sourceTable, day string, rows []models.TempLog) (string, error) {
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		return "", fmt.Errorf("invalid day %q: %w", day, err)
	}

	dir := filepath.Join(archiveDir, sourceTable, parsed.Format("2006"), parsed.Format("01"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}

	filePath := filepath.Join(dir, fmt.Sprintf("%s_%s.json", sourceTable, day))

	data, err := json.Marshal(rows)
	if err != nil {
		return "", fmt.Errorf("marshal rows: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", filePath, err)
	}

	return filePath, nil
}

// verifyArchiveFile re-reads the written file and confirms it contains the
// expected number of rows before the caller is allowed to delete from the DB.
func verifyArchiveFile(filePath string, expectedRows int) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("re-read %s: %w", filePath, err)
	}

	var rows []models.TempLog
	if err := json.Unmarshal(data, &rows); err != nil {
		return fmt.Errorf("re-parse %s: %w", filePath, err)
	}

	if len(rows) != expectedRows {
		return fmt.Errorf("row count mismatch in %s: wrote %d, read back %d", filePath, expectedRows, len(rows))
	}

	return nil
}
