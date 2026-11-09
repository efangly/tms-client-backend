package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/reports"
	"tms-backend/internal/services"
	"tms-backend/internal/utils"
)

const (
	reportQueueLen = 10
	reportTTL      = 24 * time.Hour
)

// ReportManager runs Report Jobs. Nil until StartReportManager is called.
var ReportManager *reports.Manager

// StartReportManager creates and starts the global ReportManager, writing
// Report Files under dir.
func StartReportManager(dir string) {
	if err := reports.EnsureLogoFile(); err != nil {
		utils.LogError("report - could not create logo file %s: %v", reports.LogoPath(), err)
	}
	ReportManager = reports.NewManager(dir, openReportSource, reports.MaxRows, reportQueueLen, reportTTL)
	ReportManager.Start()
}

// StopReportManager stops the global ReportManager.
func StopReportManager() {
	if ReportManager != nil {
		ReportManager.Stop()
	}
}

// CreateReportJob handles POST /api/reports/templog/jobs.
func CreateReportJob(c *fiber.Ctx) error {
	if ReportManager == nil {
		return c.Status(503).JSON(fiber.Map{"error": "report service unavailable"})
	}
	var p reports.Params
	_ = c.BodyParser(&p)
	if p.StartDate == "" {
		p.StartDate = c.Query("startDate")
	}
	if p.EndDate == "" {
		p.EndDate = c.Query("endDate")
	}
	if p.Devices == "" {
		p.Devices = c.Query("devices")
	}

	if p.StartDate == "" || p.EndDate == "" {
		return c.Status(400).JSON(fiber.Map{"error": "startDate and endDate are required"})
	}
	if _, err := time.Parse("2006-01-02", p.StartDate); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid startDate format, use YYYY-MM-DD"})
	}
	if _, err := time.Parse("2006-01-02", p.EndDate); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid endDate format, use YYYY-MM-DD"})
	}

	job, err := ReportManager.Submit(p)
	if errors.Is(err, reports.ErrQueueFull) {
		return c.Status(429).JSON(fiber.Map{"error": "too many reports are being generated, try again later"})
	}
	if err != nil {
		utils.LogError("CreateReportJob failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.Status(202).JSON(job)
}

// GetReportJob handles GET /api/reports/jobs/:id.
func GetReportJob(c *fiber.Ctx) error {
	if ReportManager == nil {
		return c.Status(503).JSON(fiber.Map{"error": "report service unavailable"})
	}
	job, ok := ReportManager.Get(c.Params("id"))
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "report job not found"})
	}
	return c.JSON(job)
}

// DownloadReportFile handles GET /api/reports/jobs/:id/file.
func DownloadReportFile(c *fiber.Ctx) error {
	if ReportManager == nil {
		return c.Status(503).JSON(fiber.Map{"error": "report service unavailable"})
	}
	job, ok := ReportManager.Get(c.Params("id"))
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "report job not found"})
	}
	path, ready := ReportManager.FilePath(job.ID)
	if !ready {
		return c.Status(409).JSON(fiber.Map{"error": "report is not ready", "status": job.Status})
	}
	// Open and stream the file ourselves: SendFile/Download keep a cached
	// handle open, which on Windows blocks deleting the file at expiry.
	f, err := os.Open(path)
	if err != nil {
		return c.Status(410).JSON(fiber.Map{"error": "report file is no longer available"})
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	c.Set(fiber.HeaderContentType, "application/pdf")
	c.Set(fiber.HeaderContentDisposition, `attachment; filename="templog-report-`+job.ID[:8]+`.pdf"`)
	return c.SendStream(f, int(st.Size()))
}

// ── data source ──────────────────────────────────────────────────────────────

// openReportSource opens the ordered row stream (machine_ip, probe_no,
// insert_time) for a Report: live temp_log rows merged with archived rows
// when the range reaches past the retention window.
func openReportSource(p reports.Params) (reports.Source, error) {
	var allMachines []models.MasterMachine
	if err := database.DB.Find(&allMachines).Error; err != nil {
		utils.LogError("report - failed to load machines: %v", err)
	}

	names := make(map[string]string, len(allMachines))
	for _, m := range allMachines {
		names[fmt.Sprintf("%s:%d", m.MachineIP, m.ProbeNo)] = m.MachineName
	}

	from, to := p.StartDate+" 00:00:00", p.EndDate+" 23:59:59"

	live := database.DB.Model(&models.TempLog{}).Where("insert_time BETWEEN ? AND ?", from, to)
	if p.Devices != "" {
		live = applyDeviceFilter(live, p.Devices, allMachines)
	}
	liveStream, err := newDBStream(live, names)
	if err != nil {
		return nil, err
	}

	cutoff := database.GetThailandTime().AddDate(0, 0, -services.RetentionDays()).Format("2006-01-02")
	if p.StartDate >= cutoff {
		return liveStream, nil
	}

	if services.GlobalArchiveService != nil {
		if _, err := services.GlobalArchiveService.RestoreRange(p.StartDate, p.EndDate); err != nil {
			liveStream.Close()
			return nil, fmt.Errorf("restore archive: %w", err)
		}
	}
	arch := database.DB.Model(&models.TempLogArchive{}).Where("insert_time BETWEEN ? AND ?", from, to)
	if p.Devices != "" {
		arch = applyDeviceFilter(arch, p.Devices, allMachines)
	}
	archStream, err := newDBStream(arch, names)
	if err != nil {
		liveStream.Close()
		return nil, err
	}
	return &mergedStream{a: liveStream, b: archStream}, nil
}

// rowStream is a peekable, ordered stream of Report rows.
type rowStream interface {
	reports.Source
	peek() (reports.Row, bool, error)
}

// dbStream streams rows of one table straight from the database cursor so
// memory stays constant regardless of result size.
type dbStream struct {
	rows  *sql.Rows
	names map[string]string
	total int64
	cur   *reports.Row
	done  bool
}

func newDBStream(q *gorm.DB, names map[string]string) (*dbStream, error) {
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, err
	}
	rows, err := q.Session(&gorm.Session{}).
		Select("machine_ip", "probe_no", "temp_value", "insert_time").
		Order("machine_ip ASC, probe_no ASC, insert_time ASC").
		Rows()
	if err != nil {
		return nil, err
	}
	return &dbStream{rows: rows, total: total, names: names}, nil
}

func (s *dbStream) Total() int64 { return s.total }
func (s *dbStream) Close() error { return s.rows.Close() }

func (s *dbStream) peek() (reports.Row, bool, error) {
	if s.cur == nil && !s.done {
		if !s.rows.Next() {
			s.done = true
			return reports.Row{}, false, s.rows.Err()
		}
		var r reports.Row
		var temp sql.NullFloat64
		if err := s.rows.Scan(&r.IP, &r.ProbeNo, &temp, &r.Time); err != nil {
			return reports.Row{}, false, err
		}
		r.MachineName = s.names[fmt.Sprintf("%s:%d", r.IP, r.ProbeNo)]
		if temp.Valid {
			v := temp.Float64
			r.Temp = &v
		}
		s.cur = &r
	}
	if s.cur == nil {
		return reports.Row{}, false, nil
	}
	return *s.cur, true, nil
}

func (s *dbStream) Next() (reports.Row, bool, error) {
	r, ok, err := s.peek()
	s.cur = nil
	return r, ok, err
}

// mergedStream merges two ordered streams, keeping (ip, probe, time) order.
type mergedStream struct{ a, b rowStream }

func (m *mergedStream) Total() int64 { return m.a.Total() + m.b.Total() }
func (m *mergedStream) Close() error {
	ea, eb := m.a.Close(), m.b.Close()
	if ea != nil {
		return ea
	}
	return eb
}

func (m *mergedStream) pick() (rowStream, reports.Row, bool, error) {
	ra, oka, err := m.a.peek()
	if err != nil {
		return nil, reports.Row{}, false, err
	}
	rb, okb, err := m.b.peek()
	if err != nil {
		return nil, reports.Row{}, false, err
	}
	switch {
	case !oka && !okb:
		return nil, reports.Row{}, false, nil
	case !okb:
		return m.a, ra, true, nil
	case !oka:
		return m.b, rb, true, nil
	case rowLess(rb, ra):
		return m.b, rb, true, nil
	default:
		return m.a, ra, true, nil
	}
}

func (m *mergedStream) Next() (reports.Row, bool, error) {
	src, _, ok, err := m.pick()
	if err != nil || !ok {
		return reports.Row{}, false, err
	}
	return src.Next()
}

func rowLess(x, y reports.Row) bool {
	if x.IP != y.IP {
		return x.IP < y.IP
	}
	if x.ProbeNo != y.ProbeNo {
		return x.ProbeNo < y.ProbeNo
	}
	return x.Time.Before(y.Time)
}
