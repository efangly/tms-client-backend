package handlers

import (
	"bufio"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/services"
	"tms-backend/internal/utils"
)

// GetDevices returns all devices
func GetDevices(c *fiber.Ctx) error {
	var devices []models.Device
	if err := database.DB.Find(&devices).Error; err != nil {
		utils.LogError("GetDevices failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(devices)
}

// GetDevice returns a single device by ID
func GetDevice(c *fiber.Ctx) error {
	id := c.Params("id")
	var device models.Device
	if err := database.DB.First(&device, "id = ?", id).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Device not found"})
	}
	return c.JSON(device)
}

// CreateDevice creates a new device
func CreateDevice(c *fiber.Ctx) error {
	device := new(models.Device)
	if err := c.BodyParser(device); err != nil {
		utils.LogError("CreateDevice - Failed to parse body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	// Generate UUID for new device
	device.ID = uuid.New().String()
	device.CreatedAt = database.GetThailandTime()
	device.UpdatedAt = database.GetThailandTime()

	if err := database.DB.Create(device).Error; err != nil {
		utils.LogError("CreateDevice - Failed to create device: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Also create master_machine entry for legacy compatibility
	for i := 1; i <= device.Probe; i++ {
		mm := models.MasterMachine{
			MachineIP:   device.IP,
			ProbeNo:     i,
			ProbeAll:    device.Probe,
			MachineName: device.Devicename,
			MinTemp:     &device.Mintemp,
			MaxTemp:     &device.Maxtemp,
			AdjTemp:     &device.Adjtemp,
		}
		database.DB.Create(&mm)
	}

	return c.Status(201).JSON(device)
}

// UpdateDevice updates a device
func UpdateDevice(c *fiber.Ctx) error {
	id := c.Params("id")

	var device models.Device
	if err := database.DB.First(&device, "id = ?", id).Error; err != nil {
		utils.LogError("UpdateDevice - Device not found (id=%s): %v", id, err)
		return c.Status(404).JSON(fiber.Map{"error": "Device not found"})
	}

	var updates models.Device
	if err := c.BodyParser(&updates); err != nil {
		utils.LogError("UpdateDevice - Failed to parse body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	updates.UpdatedAt = database.GetThailandTime()

	if err := database.DB.Model(&device).Updates(updates).Error; err != nil {
		utils.LogError("UpdateDevice - Failed to update device (id=%s): %v", id, err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Update master_machine entries
	database.DB.Model(&models.MasterMachine{}).
		Where("machine_ip = ?", device.IP).
		Updates(map[string]interface{}{
			"machine_name": updates.Devicename,
			"min_temp":     updates.Mintemp,
			"max_temp":     updates.Maxtemp,
			"adj_temp":     updates.Adjtemp,
		})

	return c.JSON(device)
}

// DeleteDevice deletes a device
func DeleteDevice(c *fiber.Ctx) error {
	id := c.Params("id")

	var device models.Device
	if err := database.DB.First(&device, "id = ?", id).Error; err != nil {
		utils.LogError("DeleteDevice - Device not found (id=%s): %v", id, err)
		return c.Status(404).JSON(fiber.Map{"error": "Device not found"})
	}

	// Delete master_machine entries
	database.DB.Where("machine_ip = ?", device.IP).Delete(&models.MasterMachine{})

	// Delete device
	if err := database.DB.Delete(&device).Error; err != nil {
		utils.LogError("DeleteDevice - Failed to delete device (id=%s): %v", id, err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
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

	// Get latest temperature for each machine
	type MachineWithTemp struct {
		models.MasterMachine
		CurrentTemp *float64 `json:"currentTemp"`
		LastUpdate  *string  `json:"lastUpdate"`
	}

	var result []MachineWithTemp
	for _, m := range machines {
		mwt := MachineWithTemp{MasterMachine: m}

		var tempLog models.TempLog
		if err := database.DB.Where("machine_ip = ? AND probe_no = ?", m.MachineIP, m.ProbeNo).
			Order("insert_time DESC").
			First(&tempLog).Error; err == nil {
			mwt.CurrentTemp = tempLog.TempValue
			lastUpdate := tempLog.InsertTime.Format("2006-01-02 15:04:05")
			mwt.LastUpdate = &lastUpdate
		}

		result = append(result, mwt)
	}

	return c.JSON(result)
}

// UpdateMachine updates a machine
func UpdateMachine(c *fiber.Ctx) error {
	machineIP := c.Params("machineIp")
	probeNo := c.Params("probeNo")

	var machine models.MasterMachine
	if err := database.DB.First(&machine, "machine_ip = ? AND probe_no = ?", machineIP, probeNo).Error; err != nil {
		utils.LogError("UpdateMachine - Machine not found (ip=%s, probe=%s): %v", machineIP, probeNo, err)
		return c.Status(404).JSON(fiber.Map{"error": "Machine not found"})
	}

	var updates map[string]interface{}
	if err := c.BodyParser(&updates); err != nil {
		utils.LogError("UpdateMachine - Failed to parse body: %v", err)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}

	if err := database.DB.Model(&machine).Updates(updates).Error; err != nil {
		utils.LogError("UpdateMachine - Failed to update machine (ip=%s, probe=%s): %v", machineIP, probeNo, err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

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

	// Filter by devices if specified
	if devices != "" {
		var deviceList []models.Device
		database.DB.Find(&deviceList, "id IN (?)", splitComma(devices))

		var ips []string
		for _, d := range deviceList {
			ips = append(ips, d.IP)
		}
		if len(ips) > 0 {
			query = query.Where("machine_ip IN ?", ips)
		}
	}

	var logs []models.TempLog
	if err := query.Order("insert_time ASC").Find(&logs).Error; err != nil {
		utils.LogError("GetTempLogReport failed: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	// Build a lookup map of machine_ip -> device_name
	var allDevices []models.Device
	database.DB.Find(&allDevices)
	deviceNameMap := make(map[string]string)
	for _, d := range allDevices {
		deviceNameMap[d.IP] = d.Devicename
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
		deviceName := deviceNameMap[logItem.MachineIP]
		if deviceName == "" {
			deviceName = logItem.MachineIP // fallback to IP
		}
		key := fmt.Sprintf("%s-P%d", deviceName, logItem.ProbeNo)
		if seriesMap[key] == nil {
			seriesMap[key] = &Series{Label: key, Data: []SeriesPoint{}}
		}
		if logItem.TempValue != nil {
			seriesMap[key].Data = append(seriesMap[key].Data, SeriesPoint{
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

	// Subscribe to events
	eventChan := services.GlobalPollingService.Subscribe()

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// Send initial connection message
		fmt.Fprintf(w, "data: {\"type\":\"connected\"}\n\n")
		w.Flush()

		// Send heartbeat every 30 seconds
		heartbeat := time.NewTicker(30 * time.Second)
		defer heartbeat.Stop()
		defer services.GlobalPollingService.Unsubscribe(eventChan)

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
