package services

import (
	"fmt"
	"log"
	"sync"
	"time"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/tcpclient"
	"tms-backend/internal/utils"
)

// Event types for SSE
type DataSavedEvent struct {
	Saved  int `json:"saved"`
	Errors int `json:"errors"`
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
	subMu                  sync.Mutex
	apiNotificationService *APINotificationService
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
		apiNotificationService: NewAPINotificationService(),
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

// Start the polling service
func (p *PollingService) Start() {
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

	// Initial poll
	log.Println("Running initial poll and save...")
	p.pollAndSave()

	// Start poll ticker
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
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
	startTime := time.Now()
	log.Println("=== Starting Poll & Save cycle ===")

	// Get all devices
	var devices []models.Device
	if err := database.DB.Find(&devices).Error; err != nil {
		utils.LogError("pollAndSave - Failed to load devices: %v", err)
		log.Printf("Error loading devices: %v", err)
		return
	}

	log.Printf("Found %d devices to poll", len(devices))

	savedCount := 0
	errorCount := 0
	now := database.GetThailandTime()
	sDate := now.Format("20060102")
	sTime := now.Format("15")

	for _, device := range devices {
		// Request data from TCP server
		response := tcpclient.RequestFromTCPServer(
			tcpclient.ServerConfig{
				IP:   device.IP,
				Port: device.Port,
				Name: device.Devicename,
			},
			"A",
			5*time.Second,
		)

		// Update device online status
		status := "Offline"
		if response.Connected && len(response.Probes) > 0 {
			status = "Online"
		}
		database.DB.Model(&device).Update("onlinestatus", status)

		// Save temperature data
		for _, probe := range response.Probes {
			// Apply temperature adjustment
			adjustedTemp := probe.TempValue + device.Adjtemp

			// Determine status based on temperature range
			tempStatus := "N" // Normal
			if adjustedTemp < device.Mintemp {
				tempStatus = "L" // Low
			} else if adjustedTemp > device.Maxtemp {
				tempStatus = "H" // High
			}

			// Convert RealValue to int (as per database schema)
			realValueInt := probe.RealValue

			// Create temp log entry
			tempLog := models.TempLog{
				MachineIP:  device.IP,
				ProbeNo:    probe.ProbeNo,
				McuID:      &probe.McuID,
				TempValue:  &adjustedTemp,
				RealValue:  &realValueInt,
				Status:     &tempStatus,
				SendTime:   &now,
				InsertTime: now,
				SDate:      &sDate,
				STime:      &sTime,
			}

			if err := database.DB.Create(&tempLog).Error; err != nil {
				utils.LogError("pollAndSave - Failed to save temp log (device=%s, probe=%d): %v", device.Devicename, probe.ProbeNo, err)
				log.Printf("Error saving temp log: %v", err)
				errorCount++
			} else {
				log.Printf("  ✅ %s Probe %d: %.2f°C", device.Devicename, probe.ProbeNo, adjustedTemp)
				savedCount++

				// ส่งข้อมูลไป Legacy API
				if p.apiNotificationService.IsLegacyAPIEnabled() {
					payload := TempLogPayload{
						McuID:     fmt.Sprintf("%s(%d)", device.Devicename, probe.ProbeNo),
						Status:    "00000110", // Normal status
						TempValue: adjustedTemp,
						RealValue: realValueInt,
						Date:      sDate,
						Time:      sTime,
					}
					go func(pl TempLogPayload) {
						if err := p.apiNotificationService.SendTempLog(pl); err != nil {
							utils.LogError("pollAndSave - Failed to send to Legacy API (device=%s, probe=%d): %v", device.Devicename, probe.ProbeNo, err)
							log.Printf("Failed to send to Legacy API: %v", err)
						}
					}(payload)
				}
			}

			// Check alerts
			p.checkDeviceAlert(device, probe.ProbeNo, adjustedTemp)
		}
	}

	elapsed := time.Since(startTime)
	log.Printf("=== Poll & Save completed in %v ===", elapsed)
	log.Printf("   Saved: %d temperature logs, %d errors", savedCount, errorCount)

	// Notify subscribers
	p.notifySubscribers(DataSavedEvent{
		Saved:  savedCount,
		Errors: errorCount,
	})
}

// checkAlerts checks for temperature alerts on current readings
func (p *PollingService) checkAlerts() {
	// Get all devices
	var devices []models.Device
	if err := database.DB.Find(&devices).Error; err != nil {
		return
	}

	for _, device := range devices {
		// Request current temperature
		response := tcpclient.RequestFromTCPServer(
			tcpclient.ServerConfig{
				IP:   device.IP,
				Port: device.Port,
				Name: device.Devicename,
			},
			"A",
			3*time.Second,
		)

		for _, probe := range response.Probes {
			adjustedTemp := probe.TempValue + device.Adjtemp
			p.checkDeviceAlert(device, probe.ProbeNo, adjustedTemp)
		}
	}
}

// checkDeviceAlert checks and records alert for a single device/probe
func (p *PollingService) checkDeviceAlert(device models.Device, probeNo int, temp float64) {
	alertKey := fmt.Sprintf("%s:%d", device.IP, probeNo)

	alertStatesMu.Lock()
	prevState := alertStates[alertKey]
	alertStatesMu.Unlock()

	var currentState string
	if temp < device.Mintemp {
		currentState = "L"
	} else if temp > device.Maxtemp {
		currentState = "H"
	} else {
		currentState = "N"
	}

	// Check for state change
	if currentState != prevState {
		now := database.GetThailandTime()
		dateStr := now.Format("20060102")
		timeStr := now.Format("15:04:05")

		// Record alert if out of range
		if currentState == "H" || currentState == "L" {
			alertTypeStr := "HIGH"
			if currentState == "L" {
				alertTypeStr = "LOW"
			}
			alertMessage := fmt.Sprintf("อุณหภูมิ%sเกิน (ค่าปัจจุบัน: %.2f°C, ช่วง: %.2f-%.2f°C) %s(%d)",
				map[string]string{"H": "สูง", "L": "ต่ำ"}[currentState],
				temp, device.Mintemp, device.Maxtemp, device.Devicename, probeNo)

			log.Printf("🚨 ALERT: %s Probe %d - Temp %.2f°C is %s (min: %.2f, max: %.2f)",
				device.Devicename, probeNo, temp, alertTypeStr,
				device.Mintemp, device.Maxtemp)

			// Create temp error record
			tempError := models.TempError{
				MachineIP:   device.IP,
				ProbeNo:     probeNo,
				MachineName: &device.Devicename,
				TempValue:   &temp,
				ErrorTime:   now,
				MinTemp:     &device.Mintemp,
				MaxTemp:     &device.Maxtemp,
				TempStatus:  &currentState,
			}
			database.DB.Create(&tempError)

			// Also log to temp_log with status
			sDate := now.Format("20060102")
			sTime := now.Format("15")
			tempLog := models.TempLog{
				MachineIP:  device.IP,
				ProbeNo:    probeNo,
				TempValue:  &temp,
				Status:     &currentState,
				InsertTime: now,
				SDate:      &sDate,
				STime:      &sTime,
			}
			database.DB.Create(&tempLog)

			// ส่ง Alert API notification
			if p.apiNotificationService.IsLegacyAPIEnabled() {
				// Get McuID from latest probe data
				response := tcpclient.RequestFromTCPServer(
					tcpclient.ServerConfig{
						IP:   device.IP,
						Port: device.Port,
						Name: device.Devicename,
					},
					"A",
					3*time.Second,
				)
				mcuID := device.Devicename
				for _, p := range response.Probes {
					if p.ProbeNo == probeNo {
						mcuID = fmt.Sprintf("%s(%d)", p.McuID, p.ProbeNo)
						break
					}
				}
				alertPayload := AlertPayload{
					McuID:       mcuID,
					Status:      map[string]string{"H": "00000010", "L": "00000011"}[currentState],
					TempValue:   temp,
					RealValue:   int(temp * 100),
					Date:        dateStr,
					Time:        timeStr,
					Message:     alertMessage,
					AlertType:   map[string]string{"H": "high", "L": "low"}[currentState],
					MachineName: device.Devicename,
					ProbeNo:     probeNo,
					MinTemp:     device.Mintemp,
					MaxTemp:     device.Maxtemp,
				}
				go func(pl AlertPayload) {
					if err := p.apiNotificationService.SendAlert(pl); err != nil {
						utils.LogError("checkDeviceAlert - Failed to send alert notification (device=%s, probe=%d): %v", pl.MachineName, pl.ProbeNo, err)
						log.Printf("  ⚠️ Failed to send alert notification: %v", err)
					} else {
						log.Printf("  🔔 Alert notification sent for %s Probe %d", pl.MachineName, pl.ProbeNo)
					}
				}(alertPayload)
			}
		}

		// Record return to normal
		if currentState == "N" && (prevState == "H" || prevState == "L") {
			normalMessage := fmt.Sprintf("อุณหภูมิกลับเข้าช่วงปกติแล้ว (ค่าปัจจุบัน: %.2f°C)", temp)
			log.Printf("✅ NORMAL: %s Probe %d - Temp %.2f°C returned to normal range",
				device.Devicename, probeNo, temp)

			now := database.GetThailandTime()
			sDate := now.Format("20060102")
			sTime := now.Format("15")
			tempLog := models.TempLog{
				MachineIP:  device.IP,
				ProbeNo:    probeNo,
				TempValue:  &temp,
				Status:     &currentState,
				InsertTime: now,
				SDate:      &sDate,
				STime:      &sTime,
			}
			database.DB.Create(&tempLog)

			// ส่ง Alert API notification ว่ากลับปกติ
			if p.apiNotificationService.IsLegacyAPIEnabled() {
				// Get McuID from latest probe data
				response := tcpclient.RequestFromTCPServer(
					tcpclient.ServerConfig{
						IP:   device.IP,
						Port: device.Port,
						Name: device.Devicename,
					},
					"A",
					3*time.Second,
				)
				mcuID := device.Devicename
				for _, p := range response.Probes {
					if p.ProbeNo == probeNo {
						mcuID = fmt.Sprintf("%s(%d)", device.Devicename, p.ProbeNo)
						break
					}
				}
				alertPayload := AlertPayload{
					McuID:       mcuID,
					Status:      "00000001", // Normal
					TempValue:   temp,
					RealValue:   int(temp * 100),
					Date:        dateStr,
					Time:        timeStr,
					Message:     normalMessage,
					AlertType:   "normal",
					MachineName: device.Devicename,
					ProbeNo:     probeNo,
					MinTemp:     device.Mintemp,
					MaxTemp:     device.Maxtemp,
				}
				go func(pl AlertPayload) {
					if err := p.apiNotificationService.SendAlert(pl); err != nil {
						utils.LogError("checkDeviceAlert - Failed to send recovery notification (device=%s, probe=%d): %v", pl.MachineName, pl.ProbeNo, err)
						log.Printf("  ⚠️ Failed to send recovery notification: %v", err)
					} else {
						log.Printf("  ✅ Recovery notification sent for %s Probe %d", pl.MachineName, pl.ProbeNo)
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

// Global polling service instance
var GlobalPollingService *PollingService

func init() {
	GlobalPollingService = NewPollingService()
}
