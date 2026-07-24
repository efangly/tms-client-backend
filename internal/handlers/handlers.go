package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/services"
	"tms-backend/internal/utils"
)

// jsonToColumn maps JSON field names sent by clients to their actual database
// column names. GORM's Updates(map) uses map keys as literal SQL column names,
// so any mismatch between the JSON tag and the gorm:"column:" tag must be
// listed here. Fields where both names are identical need not be listed.
var jsonToColumn = map[string]string{
	"probeAll":    "probe_all",
	"machineName": "machine_name",
	"minTemp":     "min_temp",
	"maxTemp":     "max_temp",
	"adjTemp":     "adj_temp",
}

// toDBColumns translates a map keyed by JSON field names into one keyed by
// database column names. Unknown keys pass through unchanged (they may already
// be column names, or will be rejected by the DB which is fine).
func toDBColumns(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if col, ok := jsonToColumn[k]; ok {
			out[col] = v
		} else {
			out[k] = v
		}
	}
	return out
}

func GetDevices(c *fiber.Ctx) error {
	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		utils.LogError("GetDevices failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(machines)
}

func GetDevice(c *fiber.Ctx) error {
	machineIP := c.Params("id")
	probeNo := c.QueryInt("probeNo", 1)

	var machine models.MasterMachine
	if err := database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}
	return c.JSON(machine)
}

func CreateDevice(c *fiber.Ctx) error {
	machine := new(models.MasterMachine)
	if err := c.BodyParser(machine); err != nil {
		utils.LogError("CreateDevice - Failed to parse body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	if machine.ProbeNo == 0 {
		machine.ProbeNo = 1
	}
	if machine.SType == "" {
		machine.SType = "t"
	}

	if err := database.DB.Create(machine).Error; err != nil {
		utils.LogError("CreateDevice - Failed to create machine: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.Status(201).JSON(machine)
}

func UpdateDevice(c *fiber.Ctx) error {
	machineIP := c.Params("id")
	probeNo := c.QueryInt("probeNo", 0)

	var machine models.MasterMachine
	var err error
	if probeNo > 0 {
		err = database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error
	} else {
		err = database.DB.First(&machine, "machine_ip = ?", machineIP).Error
	}
	if err != nil {
		utils.LogError("UpdateDevice - Machine not found (ip=%s, probe=%d): %v", machineIP, probeNo, err)
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}

	var updates map[string]any
	if err := c.BodyParser(&updates); err != nil {
		utils.LogError("UpdateDevice - Failed to parse body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	// Prevent primary key mutation (remove both JSON and DB column name variants)
	delete(updates, "machine_ip")
	delete(updates, "machineIp")
	delete(updates, "probe_no")
	delete(updates, "probeNo")

	// Translate JSON keys (e.g. "machineName") → DB column names (e.g. "machine_name")
	// so GORM's Updates(map) generates valid SQL column references.
	dbUpdates := toDBColumns(updates)

	if err := database.DB.Model(&machine).Updates(dbUpdates).Error; err != nil {
		utils.LogError("UpdateDevice - Failed to update machine (ip=%s, probe=%d): %v", machineIP, probeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}

	if err := database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machine.MachineIP, machine.ProbeNo).Error; err != nil {
		utils.LogError("UpdateDevice - Failed to re-fetch machine after update: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(machine)
}

func DeleteDevice(c *fiber.Ctx) error {
	machineIP := c.Params("id")
	probeNo := c.QueryInt("probeNo", 0)

	if probeNo > 0 {
		if err := database.DB.Delete(&models.MasterMachine{}, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error; err != nil {
			utils.LogError("DeleteDevice - Failed to delete machine (ip=%s, probe=%d): %v", machineIP, probeNo, err)
			return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
		}
	} else {
		if err := database.DB.Delete(&models.MasterMachine{}, "machine_ip = ?", machineIP).Error; err != nil {
			utils.LogError("DeleteDevice - Failed to delete machines (ip=%s): %v", machineIP, err)
			return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
		}
	}
	return c.JSON(fiber.Map{"success": true})
}

func GetMachines(c *fiber.Ctx) error {
	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		utils.LogError("GetMachines failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}

	// Fetch the latest temp log for every machine in one query instead of N+1.
	type latestLog struct {
		MachineIP  string
		ProbeNo    int
		TempValue  *float64
		InsertTime time.Time
	}
	var latestLogs []latestLog
	database.DB.Raw(`
		SELECT t1.machine_ip, t1.probe_no, t1.temp_value, t1.insert_time
		FROM temp_log t1
		INNER JOIN (
			SELECT machine_ip, probe_no, MAX(insert_time) AS max_time
			FROM temp_log
			GROUP BY machine_ip, probe_no
		) t2 ON t1.machine_ip = t2.machine_ip
		     AND t1.probe_no = t2.probe_no
		     AND t1.insert_time = t2.max_time
	`).Scan(&latestLogs)

	latestMap := make(map[string]latestLog, len(latestLogs))
	for _, ll := range latestLogs {
		key := fmt.Sprintf("%s:%d", ll.MachineIP, ll.ProbeNo)
		latestMap[key] = ll
	}

	result := make([]models.MachineWithStatus, 0, len(machines))
	for _, m := range machines {
		mws := models.MachineWithStatus{MasterMachine: m, OnlineStatus: "Offline"}
		key := fmt.Sprintf("%s:%d", m.MachineIP, m.ProbeNo)
		if ll, ok := latestMap[key]; ok {
			mws.CurrentValue = ll.TempValue
			lastUpdate := ll.InsertTime.Format("2006-01-02 15:04:05")
			mws.LastUpdate = &lastUpdate
			if time.Since(ll.InsertTime) < 10*time.Minute {
				mws.OnlineStatus = "Online"
			}
		}
		result = append(result, mws)
	}
	return c.JSON(result)
}

func UpdateMachine(c *fiber.Ctx) error {
	machineIP := c.Params("machineIp")
	probeNoStr := c.Params("probeNo")
	probeNo, _ := strconv.Atoi(probeNoStr)

	var machine models.MasterMachine
	if err := database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error; err != nil {
		utils.LogError("UpdateMachine - Machine not found (ip=%s, probe=%d): %v", machineIP, probeNo, err)
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}

	var updates map[string]any
	if err := c.BodyParser(&updates); err != nil {
		utils.LogError("UpdateMachine - Failed to parse body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	// Prevent primary key mutation (remove both JSON and DB column name variants)
	delete(updates, "machine_ip")
	delete(updates, "machineIp")
	delete(updates, "probe_no")
	delete(updates, "probeNo")

	// Translate JSON keys → DB column names before passing to GORM
	dbUpdates := toDBColumns(updates)

	if err := database.DB.Model(&machine).Updates(dbUpdates).Error; err != nil {
		utils.LogError("UpdateMachine - Failed to update machine (ip=%s, probe=%d): %v", machineIP, probeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}

	if err := database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error; err != nil {
		utils.LogError("UpdateMachine - Failed to re-fetch machine after update: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(machine)
}

func GetTempLogs(c *fiber.Ctx) error {
	startDate := c.Query("startDate")
	endDate := c.Query("endDate")
	limit := c.QueryInt("limit", 100)
	devices := c.Query("devices") // comma-separated machine IPs/names, optionally ":probeNo"
	includeArchive := c.QueryBool("includeArchive", false)

	var allMachines []models.MasterMachine
	if devices != "" {
		if err := database.DB.Find(&allMachines).Error; err != nil {
			utils.LogError("GetTempLogs - Failed to load machines: %v", err)
		}
	}

	query := database.DB.Model(&models.TempLog{})
	if startDate != "" && endDate != "" {
		query = query.Where("insert_time BETWEEN ? AND ?",
			startDate+" 00:00:00", endDate+" 23:59:59")
	}
	if devices != "" {
		query = applyDeviceFilter(query, devices, allMachines)
	}

	var logs []models.TempLog
	if err := query.Order("insert_time DESC").Limit(limit).Find(&logs).Error; err != nil {
		utils.LogError("GetTempLogs failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}

	if includeArchive && startDate != "" && endDate != "" {
		archived, err := fetchArchivedTempLogs(startDate, endDate, devices, allMachines)
		if err != nil {
			utils.LogError("GetTempLogs - archive fetch failed: %v", err)
		} else {
			logs = mergeTempLogs(logs, archived, true, limit)
		}
	}

	return c.JSON(logs)
}

func GetTempLogReport(c *fiber.Ctx) error {
	startDate := c.Query("startDate")
	endDate := c.Query("endDate")
	devices := c.Query("devices") // comma-separated machine IPs or machine names
	includeArchive := c.QueryBool("includeArchive", false)

	if startDate == "" || endDate == "" {
		return c.Status(400).JSON(fiber.Map{"error": "startDate and endDate are required"})
	}

	if _, err := time.Parse("2006-01-02", startDate); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid startDate format, use YYYY-MM-DD"})
	}
	if _, err := time.Parse("2006-01-02", endDate); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid endDate format, use YYYY-MM-DD"})
	}

	var allMachines []models.MasterMachine
	if err := database.DB.Find(&allMachines).Error; err != nil {
		utils.LogError("GetTempLogReport - Failed to load machines: %v", err)
		// Continue — machine names will fall back to IP
	}
	machineNameMap := make(map[string]string)
	for _, m := range allMachines {
		key := fmt.Sprintf("%s:%d", m.MachineIP, m.ProbeNo)
		machineNameMap[key] = m.MachineName
	}

	// Pass plain date-time strings (not time.Time) so the MySQL driver doesn't
	// reinterpret them through the connection's loc=Local timezone, which would
	// shift the window relative to insert_time's Thailand wall-clock values.
	query := database.DB.Model(&models.TempLog{}).Where("insert_time BETWEEN ? AND ?", startDate+" 00:00:00", endDate+" 23:59:59")

	if devices != "" {
		query = applyDeviceFilter(query, devices, allMachines)
	}

	var logs []models.TempLog
	if err := query.Order("insert_time ASC").Find(&logs).Error; err != nil {
		utils.LogError("GetTempLogReport failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}

	if includeArchive {
		archived, err := fetchArchivedTempLogs(startDate, endDate, devices, allMachines)
		if err != nil {
			utils.LogError("GetTempLogReport - archive fetch failed: %v", err)
		} else {
			logs = mergeTempLogs(logs, archived, false, 0)
		}
	}

	type SeriesPoint struct {
		X string  `json:"x"`
		Y float64 `json:"y"`
	}
	type Series struct {
		Label string        `json:"label"`
		Data  []SeriesPoint `json:"data"`
	}

	seriesMap := make(map[string]*Series)
	for _, logItem := range logs {
		key := fmt.Sprintf("%s:%d", logItem.MachineIP, logItem.ProbeNo)
		machineName := machineNameMap[key]
		if machineName == "" {
			machineName = logItem.MachineIP
		}
		seriesKey := fmt.Sprintf("%s-P%d", machineName, logItem.ProbeNo)
		if seriesMap[seriesKey] == nil {
			seriesMap[seriesKey] = &Series{Label: seriesKey, Data: []SeriesPoint{}}
		}
		if logItem.TempValue != nil {
			seriesMap[seriesKey].Data = append(seriesMap[seriesKey].Data, SeriesPoint{
				X: logItem.InsertTime.Format(time.RFC3339),
				Y: *logItem.TempValue,
			})
		}
	}

	var series []Series
	for _, s := range seriesMap {
		series = append(series, *s)
	}

	return c.JSON(fiber.Map{"data": logs, "series": series})
}

func GetTempErrors(c *fiber.Ctx) error {
	startDate := c.Query("startDate")
	endDate := c.Query("endDate")
	limit := c.QueryInt("limit", 100)

	query := database.DB.Model(&models.TempError{})
	if startDate != "" && endDate != "" {
		query = query.Where("error_time BETWEEN ? AND ?",
			startDate+" 00:00:00", endDate+" 23:59:59")
	}

	var errors []models.TempError
	if err := query.Order("error_time DESC").Limit(limit).Find(&errors).Error; err != nil {
		utils.LogError("GetTempErrors failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(errors)
}

// RunArchiveHandler manually triggers an archive run (moves temp_log rows
// older than the retention window into local JSON files).
func RunArchiveHandler(c *fiber.Ctx) error {
	if services.GlobalArchiveService == nil {
		return c.Status(503).JSON(fiber.Map{"error": "archive service not ready"})
	}
	result, err := services.GlobalArchiveService.RunArchive()
	if err != nil {
		utils.LogError("RunArchiveHandler failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(result)
}

// GetArchiveManifests lists archived-day entries, optionally filtered by
// startDate/endDate (YYYY-MM-DD, inclusive).
func GetArchiveManifests(c *fiber.Ctx) error {
	startDate := c.Query("startDate")
	endDate := c.Query("endDate")

	query := database.DB.Model(&models.ArchiveManifest{}).Where("source_table = ?", "temp_log")
	if startDate != "" && endDate != "" {
		query = query.Where("period_date BETWEEN ? AND ?", startDate, endDate)
	}

	var manifests []models.ArchiveManifest
	if err := query.Order("period_date DESC").Find(&manifests).Error; err != nil {
		utils.LogError("GetArchiveManifests failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(manifests)
}

// RestoreArchiveHandler loads archived temp_log data for a date range from
// local files into temp_log_archive, so it can be included in reports.
func RestoreArchiveHandler(c *fiber.Ctx) error {
	startDate := c.Query("startDate")
	endDate := c.Query("endDate")
	if startDate == "" || endDate == "" {
		return c.Status(400).JSON(fiber.Map{"error": "startDate and endDate are required"})
	}
	if _, err := time.Parse("2006-01-02", startDate); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid startDate format, use YYYY-MM-DD"})
	}
	if _, err := time.Parse("2006-01-02", endDate); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid endDate format, use YYYY-MM-DD"})
	}

	if services.GlobalArchiveService == nil {
		return c.Status(503).JSON(fiber.Map{"error": "archive service not ready"})
	}
	result, err := services.GlobalArchiveService.RestoreRange(startDate, endDate)
	if err != nil {
		utils.LogError("RestoreArchiveHandler failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(result)
}

func TriggerPoll(c *fiber.Ctx) error {
	if services.GlobalPollingService == nil {
		return c.Status(503).JSON(fiber.Map{"error": "polling service not ready"})
	}
	log.Println("Manual poll triggered")
	services.GlobalPollingService.TriggerOnce()
	return c.JSON(fiber.Map{"status": "polling started"})
}

func TemperatureStream(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Access-Control-Allow-Origin", "*")

	eventChan := services.GlobalPollingService.Subscribe()
	tempChan := services.GlobalPollingService.SubscribeTemperature()

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		fmt.Fprintf(w, "data: {\"type\":\"connected\"}\n\n")
		w.Flush()

		heartbeat := time.NewTicker(30 * time.Second)
		defer heartbeat.Stop()
		defer services.GlobalPollingService.Unsubscribe(eventChan)
		defer services.GlobalPollingService.UnsubscribeTemperature(tempChan)

		for {
			select {
			case event, ok := <-eventChan:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: {\"type\":\"refresh\",\"saved\":%d,\"errors\":%d}\n\n",
					event.Saved, event.Errors)
				if err := w.Flush(); err != nil {
					return
				}
			case tempEvents, ok := <-tempChan:
				if !ok {
					return
				}
				data, err := json.Marshal(fiber.Map{
					"type":        "temperature",
					"data":        tempEvents,
					"count":       len(tempEvents),
					"lastUpdated": time.Now().Format("2006-01-02 15:04:05"),
				})
				if err == nil {
					fmt.Fprintf(w, "data: %s\n\n", data)
					if err := w.Flush(); err != nil {
						return
					}
				}
			case <-heartbeat.C:
				fmt.Fprintf(w, ": heartbeat\n\n")
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	})

	return nil
}

func splitComma(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// applyDeviceFilter parses a comma-separated "devices" query param where each
// token is "ident" or "ident:probeNo" (ident = machine_ip or machine_name).
// A bare ident matches all probes of that device; an "ident:probeNo" token
// narrows to that single probe. Both forms may be mixed in one param.
func applyDeviceFilter(query *gorm.DB, devicesParam string, allMachines []models.MasterMachine) *gorm.DB {
	plainIPs := make(map[string]bool)
	type devicePair struct {
		ip      string
		probeNo int
	}
	var pairs []devicePair

	for _, token := range splitComma(devicesParam) {
		ident, probeNoStr, hasProbe := strings.Cut(token, ":")
		var probeNo int
		if hasProbe {
			var err error
			probeNo, err = strconv.Atoi(probeNoStr)
			if err != nil {
				continue // skip malformed "ident:probeNo" token
			}
		}

		for _, m := range allMachines {
			if m.MachineIP == ident || m.MachineName == ident {
				if hasProbe {
					pairs = append(pairs, devicePair{ip: m.MachineIP, probeNo: probeNo})
				} else {
					plainIPs[m.MachineIP] = true
				}
			}
		}
	}

	ips := make([]string, 0, len(plainIPs))
	for ip := range plainIPs {
		ips = append(ips, ip)
	}

	if len(ips) == 0 && len(pairs) == 0 {
		// No identifiers resolved (empty or entirely invalid filter) — match nothing,
		// consistent with passing an empty slice to "machine_ip IN (?)".
		return query.Where("1 = 0")
	}

	var clauses []string
	var args []any
	if len(ips) > 0 {
		clauses = append(clauses, "machine_ip IN ?")
		args = append(args, ips)
	}
	for _, p := range pairs {
		clauses = append(clauses, "(machine_ip = ? AND probe_no = ?)")
		args = append(args, p.ip, p.probeNo)
	}
	return query.Where(strings.Join(clauses, " OR "), args...)
}

// fetchArchivedTempLogs restores (if needed) and queries archived temp_log
// data for [startDate, endDate] from temp_log_archive, applying the same
// device filter as the live-table query, and returns it in TempLog shape so
// it can be merged with live rows.
func fetchArchivedTempLogs(startDate, endDate, devices string, allMachines []models.MasterMachine) ([]models.TempLog, error) {
	if services.GlobalArchiveService != nil {
		if _, err := services.GlobalArchiveService.RestoreRange(startDate, endDate); err != nil {
			return nil, err
		}
	}

	query := database.DB.Model(&models.TempLogArchive{}).
		Where("insert_time BETWEEN ? AND ?", startDate+" 00:00:00", endDate+" 23:59:59")
	if devices != "" {
		query = applyDeviceFilter(query, devices, allMachines)
	}

	var archived []models.TempLogArchive
	if err := query.Order("insert_time ASC").Find(&archived).Error; err != nil {
		return nil, err
	}

	logs := make([]models.TempLog, len(archived))
	for i, a := range archived {
		logs[i] = models.TempLog{
			MachineIP:  a.MachineIP,
			ProbeNo:    a.ProbeNo,
			McuID:      a.McuID,
			TempValue:  a.TempValue,
			RealValue:  a.RealValue,
			Status:     a.Status,
			SendTime:   a.SendTime,
			InsertTime: a.InsertTime,
			SDate:      a.SDate,
			STime:      a.STime,
		}
	}
	return logs, nil
}

// mergeTempLogs combines live and archived rows, sorts by insert time
// (descending if `descending`, ascending otherwise), and applies limit if > 0.
func mergeTempLogs(live, archived []models.TempLog, descending bool, limit int) []models.TempLog {
	all := make([]models.TempLog, 0, len(live)+len(archived))
	all = append(all, live...)
	all = append(all, archived...)

	sort.Slice(all, func(i, j int) bool {
		if descending {
			return all[i].InsertTime.After(all[j].InsertTime)
		}
		return all[i].InsertTime.Before(all[j].InsertTime)
	})

	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all
}

func isValidHHmm(s string) bool {
	if len(s) != 4 {
		return false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[2:])
	return err1 == nil && err2 == nil && h >= 0 && h <= 23 && m >= 0 && m <= 59 && m%5 == 0
}

func getMachineForSchedule(c *fiber.Ctx) (*models.MasterMachine, error) {
	machineIP := c.Params("machineIp")
	probeNo, err := strconv.Atoi(c.Params("probeNo"))
	if err != nil {
		return nil, err
	}
	var machine models.MasterMachine
	if err := database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error; err != nil {
		return nil, err
	}
	return &machine, nil
}

func GetSchedule(c *fiber.Ctx) error {
	machine, err := getMachineForSchedule(c)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}
	return c.JSON(fiber.Map{"times": machine.GetScheduleTimes()})
}

func SetSchedule(c *fiber.Ctx) error {
	machine, err := getMachineForSchedule(c)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}

	var body struct {
		Times []string `json:"times"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	seen := make(map[string]bool)
	var validated []string
	for _, t := range body.Times {
		t = strings.TrimSpace(t)
		if !isValidHHmm(t) {
			return c.Status(400).JSON(fiber.Map{"error": "invalid time format: " + t + " (expected HHmm, e.g. 0800)"})
		}
		if !seen[t] {
			seen[t] = true
			validated = append(validated, t)
		}
	}
	if len(validated) > 6 {
		return c.Status(400).JSON(fiber.Map{"error": "maximum 6 schedule times allowed"})
	}

	colorVal := strings.Join(validated, ",")
	if err := database.DB.Model(machine).Update("color", colorVal).Error; err != nil {
		utils.LogError("SetSchedule - Failed to update (ip=%s, probe=%d): %v", machine.MachineIP, machine.ProbeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	machine.Color = colorVal
	return c.JSON(fiber.Map{"times": machine.GetScheduleTimes()})
}

func AddScheduleTime(c *fiber.Ctx) error {
	machine, err := getMachineForSchedule(c)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}

	t := strings.TrimSpace(c.Params("time"))
	if !isValidHHmm(t) {
		return c.Status(400).JSON(fiber.Map{"error": "invalid time format: " + t + " (expected HHmm, e.g. 0800)"})
	}

	times := machine.GetScheduleTimes()
	for _, existing := range times {
		if existing == t {
			return c.JSON(fiber.Map{"times": times})
		}
	}
	if len(times) >= 6 {
		return c.Status(400).JSON(fiber.Map{"error": "maximum 6 schedule times allowed"})
	}
	times = append(times, t)

	colorVal := strings.Join(times, ",")
	if err := database.DB.Model(machine).Update("color", colorVal).Error; err != nil {
		utils.LogError("AddScheduleTime - Failed to update (ip=%s, probe=%d): %v", machine.MachineIP, machine.ProbeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	machine.Color = colorVal
	return c.JSON(fiber.Map{"times": machine.GetScheduleTimes()})
}

func RemoveScheduleTime(c *fiber.Ctx) error {
	machine, err := getMachineForSchedule(c)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}

	t := strings.TrimSpace(c.Params("time"))
	times := machine.GetScheduleTimes()
	var updated []string
	for _, existing := range times {
		if existing != t {
			updated = append(updated, existing)
		}
	}

	colorVal := strings.Join(updated, ",")
	if err := database.DB.Model(machine).Update("color", colorVal).Error; err != nil {
		utils.LogError("RemoveScheduleTime - Failed to update (ip=%s, probe=%d): %v", machine.MachineIP, machine.ProbeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	machine.Color = colorVal
	return c.JSON(fiber.Map{"times": machine.GetScheduleTimes()})
}
