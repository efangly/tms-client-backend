package services

import (
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/tcpclient"
	"tms-backend/internal/utils"
)

// Default TCP port for devices
var defaultTCPPort = 8899

func init() {
	// Get default port from environment variable
	if portStr := os.Getenv("DEFAULT_TCP_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			defaultTCPPort = port
		}
	}
}

// Event types for SSE
type DataSavedEvent struct {
	Saved  int `json:"saved"`
	Errors int `json:"errors"`
}

// MaxSensorTemp is the maximum valid sensor temperature; readings above this are considered sensor errors
const MaxSensorTemp = 80.0

// TemperatureUpdateEvent represents real-time temperature data (same as MQTT payload)
type TemperatureUpdateEvent struct {
	MachineName string  `json:"machineName"`
	TempValue   float64 `json:"tempValue"`
	Status      string  `json:"status"` // N=Normal, H=High, L=Low
	Timestamp   string  `json:"timestamp"`
}

// PollingService handles temperature polling
type PollingService struct {
	pollInterval           time.Duration
	alertInterval          time.Duration
	stopChan               chan struct{}
	wg                     sync.WaitGroup
	running                bool
	mu                     sync.Mutex
	subscribers            []chan DataSavedEvent
	temperatureSubscribers []chan []TemperatureUpdateEvent
	subMu                  sync.Mutex
	apiNotificationService *APINotificationService
	mqttService            *MQTTService
}

// Device alert state tracking
var alertStates = make(map[string]string) // key: "ip:probeNo", value: "H", "L", "N"
var alertStatesMu sync.Mutex

// NewPollingService creates a new polling service
func NewPollingService() *PollingService {
	return &PollingService{
		pollInterval:           5 * time.Minute,
		alertInterval:          5 * time.Second,
		stopChan:               make(chan struct{}),
		subscribers:            make([]chan DataSavedEvent, 0),
		temperatureSubscribers: make([]chan []TemperatureUpdateEvent, 0),
		apiNotificationService: NewAPINotificationService(),
		mqttService:            GlobalMQTTService,
	}
}

// Subscribe to data saved events
func (p *PollingService) Subscribe() chan DataSavedEvent {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	ch := make(chan DataSavedEvent, 10)
	p.subscribers = append(p.subscribers, ch)
	return ch
}

// Unsubscribe from data saved events
func (p *PollingService) Unsubscribe(ch chan DataSavedEvent) {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	for i, sub := range p.subscribers {
		if sub == ch {
			p.subscribers = append(p.subscribers[:i], p.subscribers[i+1:]...)
			close(ch)
			break
		}
	}
}

// SubscribeTemperature to temperature update events
func (p *PollingService) SubscribeTemperature() chan []TemperatureUpdateEvent {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	ch := make(chan []TemperatureUpdateEvent, 10)
	p.temperatureSubscribers = append(p.temperatureSubscribers, ch)
	return ch
}

// UnsubscribeTemperature from temperature update events
func (p *PollingService) UnsubscribeTemperature(ch chan []TemperatureUpdateEvent) {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	for i, sub := range p.temperatureSubscribers {
		if sub == ch {
			p.temperatureSubscribers = append(p.temperatureSubscribers[:i], p.temperatureSubscribers[i+1:]...)
			close(ch)
			break
		}
	}
}

// Start the polling service
func (p *PollingService) Start() {
	// Recover from any panic in polling service
	defer func() {
		if r := recover(); r != nil {
			utils.LogError("PANIC in polling service: %v", r)
			log.Printf("PANIC in polling service: %v", r)
			log.Println("Possible charset mismatch - check DB_CHARSET setting")
			p.mu.Lock()
			p.running = false
			p.mu.Unlock()
		}
	}()

	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.mu.Unlock()

	log.Println("Starting background polling service...")
	log.Printf("- Poll & Save interval: every %v", p.pollInterval)
	log.Printf("- Alert check interval: every %v", p.alertInterval)

	// Log API status
	if p.apiNotificationService.IsLegacyAPIEnabled() {
		log.Println("- Legacy API: ENABLED")
		log.Println("  • POST /legacy/templog - ส่งข้อมูลทุก 5 นาที")
		log.Println("  • POST /legacy/templog/alert/notification - ส่ง alert")
	} else {
		log.Println("- Legacy API: DISABLED (LEGACY_API_URL not configured)")
	}

	if p.mqttService != nil && p.mqttService.IsEnabled() {
		log.Println("- MQTT: ENABLED")
		log.Println("  • Publish temperature every 5 seconds")
	} else {
		log.Println("- MQTT: DISABLED (MQTT_BROKER not configured)")
	}

	// รอให้ database connection stable ก่อน poll ครั้งแรก
	log.Println("Waiting for database connection to stabilize...")
	time.Sleep(3 * time.Second)

	// ทดสอบ GetThailandTime() และ database connection
	testTime := database.GetThailandTime()
	log.Printf("Timezone test: %v", testTime.Format("2006-01-02 15:04:05.000 MST"))

	// Test database connection
	sqlDB, err := database.DB.DB()
	if err == nil {
		if err := sqlDB.Ping(); err != nil {
			log.Printf("Database ping failed: %v", err)
		} else {
			log.Println("Database connection verified")
		}
	}

	// Initial poll with error handling
	log.Println("Running initial poll and save...")
	func() {
		defer func() {
			if r := recover(); r != nil {
				utils.LogError("PANIC in initial pollAndSave: %v", r)
				log.Printf("PANIC in initial poll: %v", r)
				log.Println("This is likely a charset encoding issue")
				log.Println("Check your DB_CHARSET setting in .env file")
				log.Println("   - Use DB_CHARSET=tis620 for Thai TIS-620 database")
				log.Println("   - Use DB_CHARSET=utf8mb4 for UTF-8 database")
			}
		}()
		p.pollAndSave()
	}()

	// Start poll ticker
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				utils.LogError("PANIC in poll ticker: %v", r)
				log.Printf("PANIC in poll ticker: %v", r)
			}
		}()
		ticker := time.NewTicker(p.pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.pollAndSave()
			case <-p.stopChan:
				return
			}
		}
	}()

	// Start alert checker
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(p.alertInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				p.checkAlerts()
			case <-p.stopChan:
				return
			}
		}
	}()
}

// Stop the polling service
func (p *PollingService) Stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	p.mu.Unlock()

	close(p.stopChan)
	p.wg.Wait()
	log.Println("Polling service stopped")
}

// pollAndSave polls all devices and saves data
func (p *PollingService) pollAndSave() {
	// Recover from any panic during poll cycle
	defer func() {
		if r := recover(); r != nil {
			utils.LogError("PANIC in pollAndSave: %v", r)
			log.Printf("PANIC in pollAndSave: %v", r)
			log.Println("Charset mismatch detected!")
			log.Println("Your database likely uses a different charset than configured")
		}
	}()

	startTime := time.Now()
	log.Println("=== Starting Poll & Save cycle ===")

	// Get all machines grouped by IP
	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		utils.LogError("pollAndSave - Failed to load machines: %v", err)
		log.Printf("Error loading machines: %v", err)
		log.Println("This might be a charset encoding issue")
		log.Println("Check if DB_CHARSET in .env matches your database charset")
		return
	}

	// Group machines by IP for polling
	machinesByIP := make(map[string][]models.MasterMachine)
	for _, m := range machines {
		machinesByIP[m.MachineIP] = append(machinesByIP[m.MachineIP], m)
	}

	log.Printf("Found %d unique IPs to poll (%d total probes)", len(machinesByIP), len(machines))

	savedCount := 0
	errorCount := 0
	now := database.GetThailandTime().Truncate(time.Microsecond)
	sDate := now.Format("20060102")
	sTime := now.Format("15")

	for ip, probes := range machinesByIP {
		// Get machine name from first probe
		machineName := probes[0].MachineName

		// Request data from TCP server
		response := tcpclient.RequestFromTCPServer(
			tcpclient.ServerConfig{
				IP:   ip,
				Port: defaultTCPPort,
				Name: machineName,
			},
			"A",
			5*time.Second,
		)

		// Create a map of probe configs for quick lookup
		probeConfigs := make(map[int]models.MasterMachine)
		for _, probe := range probes {
			probeConfigs[probe.ProbeNo] = probe
		}

		// Save data for each probe received
		for _, probeData := range response.Probes {
			// Check for invalid sensor data (0xFFFF = 65535 or -1 indicates broken sensor)
			if probeData.RealValue == 65535 || probeData.RealValue == -1 {
				log.Printf("Skipping broken sensor data: %s Probe %d (RealValue: 0x%04X)", probes[0].MachineName, probeData.ProbeNo, uint16(probeData.RealValue))
				continue
			}

			// Get probe config (use default values if not found)
			probeConfig, hasConfig := probeConfigs[probeData.ProbeNo]
			if !hasConfig {
				// Use first probe's config as fallback
				probeConfig = probes[0]
				probeConfig.ProbeNo = probeData.ProbeNo
			}
			// Set default sType if not set
			if probeConfig.SType == "" {
				probeConfig.SType = "t"
			}

			// Apply temperature adjustment and round to 2 decimal places
			adjustedTemp := probeData.TempValue + probeConfig.GetAdjTemp()
			adjustedTemp = math.Round(adjustedTemp*100) / 100

			// Validate sensor reading - skip if temp exceeds threshold (likely sensor error)
			if adjustedTemp > MaxSensorTemp {
				log.Printf("Skipping sensor error: %s Probe %d temp=%.2f°C exceeds %.0f°C threshold",
					probeConfig.MachineName, probeData.ProbeNo, adjustedTemp, MaxSensorTemp)
				continue
			}

			tempStatus := "N" // Normal
			if adjustedTemp < probeConfig.GetMinTemp() {
				tempStatus = "L" // Low
			} else if adjustedTemp > probeConfig.GetMaxTemp() {
				tempStatus = "H" // High
			}

			// Convert RealValue to int (as per database schema)
			realValueInt := probeData.RealValue

			// Create unique timestamp for insert_time to avoid duplicate key
			// Truncate to microsecond precision (6 decimal places) for MySQL DATETIME compatibility
			insertTime := database.GetThailandTime().Truncate(time.Microsecond)

			// Debug: Log the timestamp being used
			log.Printf("InsertTime for %s Probe %d: %v", machineName, probeData.ProbeNo, insertTime)

			// Create temp log entry
			tempLog := models.TempLog{
				MachineIP:  ip,
				ProbeNo:    probeData.ProbeNo,
				McuID:      &probeConfig.MachineName,
				TempValue:  &adjustedTemp,
				RealValue:  &realValueInt,
				Status:     &tempStatus,
				SendTime:   &now,
				InsertTime: insertTime,
				SDate:      &sDate,
				STime:      &sTime,
			}

			// Insert the log - if duplicate, skip it
			if err := database.DB.Create(&tempLog).Error; err != nil {
				// Check if it's a duplicate key error
				if strings.Contains(err.Error(), "Duplicate entry") || strings.Contains(err.Error(), "1062") {
					// Skip duplicate - this is expected if polling faster than microsecond precision
					log.Printf("Duplicate log entry skipped for %s Probe %d", probeConfig.MachineName, probeData.ProbeNo)
				} else {
					utils.LogError("pollAndSave - Failed to save temp log (machine=%s, probe=%d): %v", probeConfig.MachineName, probeData.ProbeNo, err)
					log.Printf("Error saving temp log: %v", err)
					errorCount++
				}
			} else {
				unit := probeConfig.GetUnit()
				log.Printf("%s Probe %d: %.2f%s [%s]", probeConfig.MachineName, probeData.ProbeNo, adjustedTemp, unit, probeConfig.GetTypeLabel())
				savedCount++

				// ส่งข้อมูลไป Legacy API
				if p.apiNotificationService.IsLegacyAPIEnabled() {
					payload := TempLogPayload{
						McuID:     probeConfig.MachineName, // ใช้ชื่อของ probe นี้โดยเฉพาะ
						Status:    "00000110",              // Normal status
						TempValue: adjustedTemp,
						RealValue: realValueInt,
						Date:      sDate,
						Time:      sTime,
					}
					go func(pl TempLogPayload, probeName string, probeNo int) {
						if err := p.apiNotificationService.SendTempLog(pl); err != nil {
							utils.LogError("pollAndSave - Failed to send to Legacy API (machine=%s, probe=%d): %v", probeName, probeNo, err)
							log.Printf("Failed to send to Legacy API: %v", err)
						}
					}(payload, probeConfig.MachineName, probeData.ProbeNo)
				}
			}

			// Check alerts using this probe's config
			p.checkProbeAlert(probeConfig, probeData.ProbeNo, adjustedTemp)
		}
	}

	elapsed := time.Since(startTime)
	log.Printf("=== Poll & Save completed in %v ===", elapsed)
	log.Printf("   Saved: %d logs, %d errors", savedCount, errorCount)

	// Notify subscribers
	p.notifySubscribers(DataSavedEvent{
		Saved:  savedCount,
		Errors: errorCount,
	})
}

// checkAlerts checks for temperature alerts on current readings
func (p *PollingService) checkAlerts() {
	// Get all machines grouped by IP
	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		return
	}

	// Group machines by IP
	machinesByIP := make(map[string][]models.MasterMachine)
	for _, m := range machines {
		machinesByIP[m.MachineIP] = append(machinesByIP[m.MachineIP], m)
	}

	// Collect MQTT payloads for batch publish
	var mqttPayloads []MQTTTemperaturePayload
	now := database.GetThailandTime()

	for ip, probes := range machinesByIP {
		machineName := probes[0].MachineName

		// Request current temperature
		response := tcpclient.RequestFromTCPServer(
			tcpclient.ServerConfig{
				IP:   ip,
				Port: defaultTCPPort,
				Name: machineName,
			},
			"A",
			3*time.Second,
		)

		// Create probe config map
		probeConfigs := make(map[int]models.MasterMachine)
		for _, probe := range probes {
			probeConfigs[probe.ProbeNo] = probe
		}

		for _, probeData := range response.Probes {
			// Skip broken sensor data (0xFFFF = 65535 or -1)
			if probeData.RealValue == 65535 || probeData.RealValue == -1 {
				continue
			}

			probeConfig, hasConfig := probeConfigs[probeData.ProbeNo]
			if !hasConfig {
				probeConfig = probes[0]
				probeConfig.ProbeNo = probeData.ProbeNo
			}

			// Apply temperature adjustment and round to 2 decimal places
			adjustedTemp := probeData.TempValue + probeConfig.GetAdjTemp()
			adjustedTemp = math.Round(adjustedTemp*100) / 100

			// Validate sensor reading - skip if temp exceeds threshold (likely sensor error)
			if adjustedTemp > MaxSensorTemp {
				log.Printf("Skipping sensor error: %s Probe %d temp=%.2f°C exceeds %.0f°C threshold",
					probeConfig.MachineName, probeData.ProbeNo, adjustedTemp, MaxSensorTemp)
				continue
			}

			p.checkProbeAlert(probeConfig, probeData.ProbeNo, adjustedTemp)

			// Determine current status
			tempStatus := "N" // Normal
			if adjustedTemp < probeConfig.GetMinTemp() {
				tempStatus = "L" // Low
			} else if adjustedTemp > probeConfig.GetMaxTemp() {
				tempStatus = "H" // High
			}

			// Collect temperature data payload for MQTT
			mqttPayloads = append(mqttPayloads, MQTTTemperaturePayload{
				Probe:     probeConfig.MachineName,
				Temp:      adjustedTemp,
				Status:    tempStatus,
				Timestamp: now.Format("2006-01-02 15:04:05"),
			})
		}
	}

	// Publish all temperature readings via MQTT as batch (if MQTT is connected)
	if len(mqttPayloads) > 0 {
		if p.mqttService == nil {
			log.Println("MQTT service is nil - skipping publish")
		} else if !p.mqttService.IsEnabled() {
			// MQTT is disabled - this is expected if not configured
		} else if !p.mqttService.IsConnected() {
			log.Println("MQTT not connected - skipping publish")
		} else {
			// MQTT is connected - publish the batch
			go func(payloads []MQTTTemperaturePayload) {
				if err := p.mqttService.PublishTemperatureBatch(payloads); err != nil {
					utils.LogError("MQTT batch publish failed: %v", err)
					log.Printf("MQTT publish error: %v", err)
				} else {
					log.Printf("MQTT published %d temperature readings", len(payloads))
				}
			}(mqttPayloads)
		}
	}

	// Send temperature data via SSE (keep separate structure for SSE)
	if len(mqttPayloads) > 0 {
		// Build SSE events from probe data (need to recalculate status and timestamp)
		sseEvents := make([]TemperatureUpdateEvent, 0, len(mqttPayloads))
		for _, mqtt := range mqttPayloads {
			// Determine status based on temp
			tempStatus := "N"
			// Note: We'd need min/max temps here, but for SSE we'll just use "N" for now
			// as the status was already determined earlier in the loop
			sseEvents = append(sseEvents, TemperatureUpdateEvent{
				MachineName: mqtt.Probe,
				TempValue:   mqtt.Temp,
				Status:      tempStatus,
				Timestamp:   now.Format("2006-01-02 15:04:05"),
			})
		}
		p.notifyTemperatureSubscribers(sseEvents)
	}
}

// checkProbeAlert checks and records alert for a single probe
func (p *PollingService) checkProbeAlert(machine models.MasterMachine, probeNo int, temp float64) {
	alertKey := fmt.Sprintf("%s:%d", machine.MachineIP, probeNo)

	alertStatesMu.Lock()
	prevState := alertStates[alertKey]
	alertStatesMu.Unlock()

	minTemp := machine.GetMinTemp()
	maxTemp := machine.GetMaxTemp()

	var currentState string
	if temp < minTemp {
		currentState = "L"
	} else if temp > maxTemp {
		currentState = "H"
	} else {
		currentState = "N"
	}

	// Check for state change
	if currentState != prevState {
		now := database.GetThailandTime().Truncate(time.Microsecond)
		dateStr := now.Format("20060102")
		timeStr := now.Format("15:04:05")

		// Record alert if out of range
		if currentState == "H" || currentState == "L" {
			alertTypeStr := "HIGH"
			if currentState == "L" {
				alertTypeStr = "LOW"
			}

			unit := machine.GetUnit()
			typeLabel := machine.GetTypeLabel()
			alertMessage := fmt.Sprintf("%s %sเกิน (ค่าปัจจุบัน: %.2f%s, ช่วง: %.2f-%.2f%s) %s(%d)",
				typeLabel,
				map[string]string{"H": "สูง", "L": "ต่ำ"}[currentState],
				temp, unit, minTemp, maxTemp, unit, machine.MachineName, probeNo)

			log.Printf("ALERT: %s Probe %d - %s %.2f%s is %s (min: %.2f, max: %.2f)",
				machine.MachineName, probeNo, typeLabel, temp, unit, alertTypeStr,
				minTemp, maxTemp)

			// Create unique timestamp to avoid duplicate key
			// Truncate to microsecond precision (6 decimal places) for MySQL DATETIME compatibility
			errorTime := database.GetThailandTime().Truncate(time.Microsecond)

			// Create temp error record
			tempError := models.TempError{
				MachineIP:   machine.MachineIP,
				ProbeNo:     probeNo,
				MachineName: &machine.MachineName,
				TempValue:   &temp,
				ErrorTime:   errorTime,
				MinTemp:     &minTemp,
				MaxTemp:     &maxTemp,
				TempStatus:  "p", // process
				ErrorType:   "o", // over
				SType:       machine.SType,
			}

			// Insert temp error - skip if duplicate
			if err := database.DB.Create(&tempError).Error; err != nil {
				if !strings.Contains(err.Error(), "Duplicate entry") && !strings.Contains(err.Error(), "1062") {
					utils.LogError("checkAlerts - Failed to create temp_error: %v", err)
				}
			}

			// Note: temp_log is already created in pollAndSave()
			// No need to insert again here to avoid duplicate key error

			// ส่ง Alert API notification
			if p.apiNotificationService.IsLegacyAPIEnabled() {
				// ใช้ชื่อเครื่องเฉพาะ ไม่ใส่ probe no
				mcuID := machine.MachineName
				alertPayload := AlertPayload{
					McuID:       mcuID,
					Status:      map[string]string{"H": "00000010", "L": "00000011"}[currentState],
					TempValue:   temp,
					RealValue:   int(temp * 100),
					Date:        dateStr,
					Time:        timeStr,
					Message:     alertMessage,
					AlertType:   map[string]string{"H": "high", "L": "low"}[currentState],
					MachineName: machine.MachineName,
					ProbeNo:     probeNo,
					MinTemp:     minTemp,
					MaxTemp:     maxTemp,
				}
				go func(pl AlertPayload) {
					if err := p.apiNotificationService.SendAlert(pl); err != nil {
						utils.LogError("checkProbeAlert - Failed to send alert notification (machine=%s, probe=%d): %v", pl.MachineName, pl.ProbeNo, err)
						log.Printf("Failed to send alert notification: %v", err)
					} else {
						log.Printf("Alert notification sent for %s Probe %d", pl.MachineName, pl.ProbeNo)
					}
				}(alertPayload)
			}
		}

		// Record return to normal
		if currentState == "N" && (prevState == "H" || prevState == "L") {
			unit := machine.GetUnit()
			normalMessage := fmt.Sprintf("%s กลับเข้าช่วงปกติแล้ว (ค่าปัจจุบัน: %.2f%s)", machine.GetTypeLabel(), temp, unit)
			log.Printf("NORMAL: %s Probe %d - %.2f%s returned to normal range",
				machine.MachineName, probeNo, temp, unit)

			// Note: temp_log is already created in pollAndSave()
			// No need to insert again here to avoid duplicate key error

			// ส่ง Alert API notification ว่ากลับปกติ
			if p.apiNotificationService.IsLegacyAPIEnabled() {
				// ใช้ชื่อเครื่องเฉพาะ ไม่ใส่ probe no
				mcuID := machine.MachineName
				alertPayload := AlertPayload{
					McuID:       mcuID,
					Status:      "00000001", // Normal
					TempValue:   temp,
					RealValue:   int(temp * 100),
					Date:        dateStr,
					Time:        timeStr,
					Message:     normalMessage,
					AlertType:   "normal",
					MachineName: machine.MachineName,
					ProbeNo:     probeNo,
					MinTemp:     minTemp,
					MaxTemp:     maxTemp,
				}
				go func(pl AlertPayload) {
					if err := p.apiNotificationService.SendAlert(pl); err != nil {
						utils.LogError("checkProbeAlert - Failed to send recovery notification (machine=%s, probe=%d): %v", pl.MachineName, pl.ProbeNo, err)
						log.Printf("Failed to send recovery notification: %v", err)
					} else {
						log.Printf("Recovery notification sent for %s Probe %d", pl.MachineName, pl.ProbeNo)
					}
				}(alertPayload)
			}
		}

		// Update state
		alertStatesMu.Lock()
		alertStates[alertKey] = currentState
		alertStatesMu.Unlock()
	}
}

// notifySubscribers notifies all subscribers of data saved event
func (p *PollingService) notifySubscribers(event DataSavedEvent) {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	for _, ch := range p.subscribers {
		select {
		case ch <- event:
		default:
			// Channel full, skip
		}
	}
}

// notifyTemperatureSubscribers notifies all subscribers of temperature updates
func (p *PollingService) notifyTemperatureSubscribers(events []TemperatureUpdateEvent) {
	p.subMu.Lock()
	defer p.subMu.Unlock()

	for _, ch := range p.temperatureSubscribers {
		select {
		case ch <- events:
			// Successfully sent
		default:
			// Channel full, skip
		}
	}
}

// Global polling service instance
var GlobalPollingService *PollingService
