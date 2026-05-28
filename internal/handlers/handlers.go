package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/services"
	"tms-backend/internal/utils"
)

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

	// Prevent primary key mutation
	delete(updates, "machine_ip")
	delete(updates, "machineIp")
	delete(updates, "probe_no")
	delete(updates, "probeNo")

	if err := database.DB.Model(&machine).Updates(updates).Error; err != nil {
		utils.LogError("UpdateDevice - Failed to update machine (ip=%s, probe=%d): %v", machineIP, probeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}

	database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machine.MachineIP, machine.ProbeNo)
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

	var updates map[string]interface{}
	if err := c.BodyParser(&updates); err != nil {
		utils.LogError("UpdateMachine - Failed to parse body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	// Prevent primary key mutation
	delete(updates, "machine_ip")
	delete(updates, "machineIp")
	delete(updates, "probe_no")
	delete(updates, "probeNo")

	if err := database.DB.Model(&machine).Updates(updates).Error; err != nil {
		utils.LogError("UpdateMachine - Failed to update machine (ip=%s, probe=%d): %v", machineIP, probeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}

	database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo)
	return c.JSON(machine)
}

func GetTempLogs(c *fiber.Ctx) error {
	startDate := c.Query("startDate")
	endDate := c.Query("endDate")
	limit := c.QueryInt("limit", 100)

	query := database.DB.Model(&models.TempLog{})
	if startDate != "" && endDate != "" {
		query = query.Where("insert_time BETWEEN ? AND ?",
			startDate+" 00:00:00", endDate+" 23:59:59")
	}

	var logs []models.TempLog
	if err := query.Order("insert_time DESC").Limit(limit).Find(&logs).Error; err != nil {
		utils.LogError("GetTempLogs failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
	}
	return c.JSON(logs)
}

func GetTempLogReport(c *fiber.Ctx) error {
	startDate := c.Query("startDate")
	endDate := c.Query("endDate")
	devices := c.Query("devices") // comma-separated machine IPs

	if startDate == "" || endDate == "" {
		return c.Status(400).JSON(fiber.Map{"error": "startDate and endDate are required"})
	}

	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	end = end.Add(24*time.Hour - time.Second)

	query := database.DB.Model(&models.TempLog{}).Where("insert_time BETWEEN ? AND ?", start, end)

	if devices != "" {
		ips := splitComma(devices)
		if len(ips) > 0 {
			query = query.Where("machine_ip IN ?", ips)
		}
	}

	var logs []models.TempLog
	if err := query.Order("insert_time ASC").Find(&logs).Error; err != nil {
		utils.LogError("GetTempLogReport failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "internal server error"})
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

func TriggerPoll(c *fiber.Ctx) error {
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
