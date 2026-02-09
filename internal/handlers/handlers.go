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

// GetDevices returns all machines (grouped by IP)
func GetDevices(c *fiber.Ctx) error {
	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		utils.LogError("GetDevices failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(machines)
}

// GetDevice returns a single machine by IP and probe
func GetDevice(c *fiber.Ctx) error {
	machineIP := c.Params("id") // id is actually machineIP
	probeNo := c.QueryInt("probeNo", 1)

	var machine models.MasterMachine
	if err := database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}
	return c.JSON(machine)
}

// CreateDevice creates a new machine entry
func CreateDevice(c *fiber.Ctx) error {
	machine := new(models.MasterMachine)
	if err := c.BodyParser(machine); err != nil {
		utils.LogError("CreateDevice - Failed to parse body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	// Set default probe_no if not provided
	if machine.ProbeNo == 0 {
		machine.ProbeNo = 1
	}

	// Set default sType if not provided
	if machine.SType == "" {
		machine.SType = "t"
	}

	if err := database.DB.Create(machine).Error; err != nil {
		utils.LogError("CreateDevice - Failed to create machine: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.Status(201).JSON(machine)
}

// UpdateDevice updates a machine
func UpdateDevice(c *fiber.Ctx) error {
	machineIP := c.Params("id") // id is actually machineIP
	probeNo := c.QueryInt("probeNo", 0)

	var machine models.MasterMachine
	var err error

	if probeNo > 0 {
		// Update specific probe
		err = database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error
	} else {
		// Update first probe found for this IP
		err = database.DB.First(&machine, "machine_ip = ?", machineIP).Error
	}

	if err != nil {
		utils.LogError("UpdateDevice - Machine not found (ip=%s, probe=%d): %v", machineIP, probeNo, err)
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}

	var updates map[string]interface{}
	if err := c.BodyParser(&updates); err != nil {
		utils.LogError("UpdateDevice - Failed to parse body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	if err := database.DB.Model(&machine).Updates(updates).Error; err != nil {
		utils.LogError("UpdateDevice - Failed to update machine (ip=%s, probe=%d): %v", machineIP, probeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Reload the updated machine
	database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machine.MachineIP, machine.ProbeNo)

	return c.JSON(machine)
}

// DeleteDevice deletes a machine entry
func DeleteDevice(c *fiber.Ctx) error {
	machineIP := c.Params("id") // id is actually machineIP
	probeNo := c.QueryInt("probeNo", 0)

	if probeNo > 0 {
		// Delete specific probe
		if err := database.DB.Delete(&models.MasterMachine{}, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error; err != nil {
			utils.LogError("DeleteDevice - Failed to delete machine (ip=%s, probe=%d): %v", machineIP, probeNo, err)
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
	} else {
		// Delete all probes for this IP
		if err := database.DB.Delete(&models.MasterMachine{}, "machine_ip = ?", machineIP).Error; err != nil {
			utils.LogError("DeleteDevice - Failed to delete machines (ip=%s): %v", machineIP, err)
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
	}

	return c.JSON(fiber.Map{"success": true})
}

// GetMachines returns all machines with latest temperature
func GetMachines(c *fiber.Ctx) error {
	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		utils.LogError("GetMachines failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Get latest value for each machine
	var result []models.MachineWithStatus
	for _, m := range machines {
		mws := models.MachineWithStatus{
			MasterMachine: m,
			OnlineStatus:  "Offline",
		}

		var tempLog models.TempLog
		if err := database.DB.Where("machine_ip = ? AND probe_no = ?", m.MachineIP, m.ProbeNo).
			Order("insert_time DESC").
			First(&tempLog).Error; err == nil {
			mws.CurrentValue = tempLog.TempValue
			lastUpdate := tempLog.InsertTime.Format("2006-01-02 15:04:05")
			mws.LastUpdate = &lastUpdate

			// Check if online (last update within 10 minutes)
			if time.Since(tempLog.InsertTime) < 10*time.Minute {
				mws.OnlineStatus = "Online"
			}
		}

		result = append(result, mws)
	}

	return c.JSON(result)
}

// UpdateMachine updates a machine
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

	if err := database.DB.Model(&machine).Updates(updates).Error; err != nil {
		utils.LogError("UpdateMachine - Failed to update machine (ip=%s, probe=%d): %v", machineIP, probeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Reload the updated machine
	database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo)

	return c.JSON(machine)
}

// GetTempLogs returns temperature logs
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
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(logs)
}

// GetTempLogReport returns temperature logs for report
func GetTempLogReport(c *fiber.Ctx) error {
	startDate := c.Query("startDate")
	endDate := c.Query("endDate")
	devices := c.Query("devices") // comma-separated device IDs

	if startDate == "" || endDate == "" {
		return c.Status(400).JSON(fiber.Map{"error": "startDate and endDate are required"})
	}

	// Parse dates
	start, _ := time.Parse("2006-01-02", startDate)
	end, _ := time.Parse("2006-01-02", endDate)
	end = end.Add(24*time.Hour - time.Second) // Include entire end day

	query := database.DB.Model(&models.TempLog{}).
		Where("insert_time BETWEEN ? AND ?", start, end)

	// Filter by devices if specified (now using machine_ip)
	if devices != "" {
		ips := splitComma(devices)
		if len(ips) > 0 {
			query = query.Where("machine_ip IN ?", ips)
		}
	}

	var logs []models.TempLog
	if err := query.Order("insert_time ASC").Find(&logs).Error; err != nil {
		utils.LogError("GetTempLogReport failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Build a lookup map of machine_ip:probe_no -> machine_name
	var allMachines []models.MasterMachine
	database.DB.Find(&allMachines)
	machineNameMap := make(map[string]string)
	for _, m := range allMachines {
		key := fmt.Sprintf("%s:%d", m.MachineIP, m.ProbeNo)
		machineNameMap[key] = m.MachineName
	}

	// Build series for chart
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
			machineName = logItem.MachineIP // fallback to IP
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

	return c.JSON(fiber.Map{
		"data":   logs,
		"series": series,
	})
}

// GetTempErrors returns temperature errors
func GetTempErrors(c *fiber.Ctx) error {
	var errors []models.TempError
	if err := database.DB.Order("error_time DESC").Limit(100).Find(&errors).Error; err != nil {
		utils.LogError("GetTempErrors failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(errors)
}

// TriggerPoll manually triggers a poll
func TriggerPoll(c *fiber.Ctx) error {
	log.Println("Manual poll triggered")

	// Run poll in goroutine
	go func() {
		// Access global polling service
		services.GlobalPollingService.Start()
	}()

	return c.JSON(fiber.Map{"status": "polling started"})
}

// TemperatureStream handles SSE for real-time updates
func TemperatureStream(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Access-Control-Allow-Origin", "*")

	// Subscribe to both data saved events and temperature updates from polling service
	eventChan := services.GlobalPollingService.Subscribe()
	tempChan := services.GlobalPollingService.SubscribeTemperature()

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// Send initial connection message
		fmt.Fprintf(w, "data: {\"type\":\"connected\"}\n\n")
		w.Flush()

		// Send heartbeat every 30 seconds
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
				// Send temperature data from polling service (every 5 seconds)
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

// Helper function to split comma-separated string
func splitComma(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}
