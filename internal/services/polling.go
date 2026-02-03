package services

import (
	"fmt"
	"log"
	"os"
	"strconv"
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

	// Get all machines grouped by IP
	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		utils.LogError("pollAndSave - Failed to load machines: %v", err)
		log.Printf("Error loading machines: %v", err)
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
	now := database.GetThailandTime()
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

			// Apply temperature adjustment
			adjustedTemp := probeData.TempValue + probeConfig.GetAdjTemp()

			tempStatus := "N" // Normal
			if adjustedTemp < probeConfig.GetMinTemp() {
				tempStatus = "L" // Low
			} else if adjustedTemp > probeConfig.GetMaxTemp() {
				tempStatus = "H" // High
			}

			// Convert RealValue to int (as per database schema)
			realValueInt := probeData.RealValue

			// Create temp log entry
			tempLog := models.TempLog{
				MachineIP:  ip,
				ProbeNo:    probeData.ProbeNo,
				McuID:      &probeData.McuID,
				TempValue:  &adjustedTemp,
				RealValue:  &realValueInt,
				Status:     &tempStatus,
				SendTime:   &now,
				InsertTime: now,
				SDate:      &sDate,
				STime:      &sTime,
			}

			if err := database.DB.Create(&tempLog).Error; err != nil {
				utils.LogError("pollAndSave - Failed to save temp log (machine=%s, probe=%d): %v", machineName, probeData.ProbeNo, err)
				log.Printf("Error saving temp log: %v", err)
				errorCount++
			} else {
				unit := probeConfig.GetUnit()
				log.Printf("  ✅ %s Probe %d: %.2f%s [%s]", machineName, probeData.ProbeNo, adjustedTemp, unit, probeConfig.GetTypeLabel())
				savedCount++

				// ส่งข้อมูลไป Legacy API
				if p.apiNotificationService.IsLegacyAPIEnabled() {
					payload := TempLogPayload{
						McuID:     fmt.Sprintf("%s(%d)", machineName, probeData.ProbeNo),
						Status:    "00000110", // Normal status
						TempValue: adjustedTemp,
						RealValue: realValueInt,
						Date:      sDate,
						Time:      sTime,
					}
					go func(pl TempLogPayload) {
						if err := p.apiNotificationService.SendTempLog(pl); err != nil {
							utils.LogError("pollAndSave - Failed to send to Legacy API (machine=%s, probe=%d): %v", machineName, probeData.ProbeNo, err)
							log.Printf("Failed to send to Legacy API: %v", err)
						}
					}(payload)
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
			probeConfig, hasConfig := probeConfigs[probeData.ProbeNo]
			if !hasConfig {
				probeConfig = probes[0]
				probeConfig.ProbeNo = probeData.ProbeNo
			}

			adjustedTemp := probeData.TempValue + probeConfig.GetAdjTemp()
			p.checkProbeAlert(probeConfig, probeData.ProbeNo, adjustedTemp)
		}
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
		now := database.GetThailandTime()
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

			log.Printf("🚨 ALERT: %s Probe %d - %s %.2f%s is %s (min: %.2f, max: %.2f)",
				machine.MachineName, probeNo, typeLabel, temp, unit, alertTypeStr,
				minTemp, maxTemp)

			// Create temp error record
			tempError := models.TempError{
				MachineIP:   machine.MachineIP,
				ProbeNo:     probeNo,
				MachineName: &machine.MachineName,
				TempValue:   &temp,
				ErrorTime:   now,
				MinTemp:     &minTemp,
				MaxTemp:     &maxTemp,
				TempStatus:  "p", // process
				ErrorType:   "o", // over
				SType:       machine.SType,
			}
			database.DB.Create(&tempError)

			// Note: temp_log is already created in pollAndSave()
			// No need to insert again here to avoid duplicate key error

			// ส่ง Alert API notification
			if p.apiNotificationService.IsLegacyAPIEnabled() {
				// Get McuID from latest probe data
				response := tcpclient.RequestFromTCPServer(
					tcpclient.ServerConfig{
						IP:   machine.MachineIP,
						Port: defaultTCPPort,
						Name: machine.MachineName,
					},
					"A",
					3*time.Second,
				)
				mcuID := machine.MachineName
				for _, pd := range response.Probes {
					if pd.ProbeNo == probeNo {
						mcuID = fmt.Sprintf("%s(%d)", pd.McuID, pd.ProbeNo)
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
					MachineName: machine.MachineName,
					ProbeNo:     probeNo,
					MinTemp:     minTemp,
					MaxTemp:     maxTemp,
				}
				go func(pl AlertPayload) {
					if err := p.apiNotificationService.SendAlert(pl); err != nil {
						utils.LogError("checkProbeAlert - Failed to send alert notification (machine=%s, probe=%d): %v", pl.MachineName, pl.ProbeNo, err)
						log.Printf("  ⚠️ Failed to send alert notification: %v", err)
					} else {
						log.Printf("  🔔 Alert notification sent for %s Probe %d", pl.MachineName, pl.ProbeNo)
					}
				}(alertPayload)
			}
		}

		// Record return to normal
		if currentState == "N" && (prevState == "H" || prevState == "L") {
			unit := machine.GetUnit()
			normalMessage := fmt.Sprintf("%s กลับเข้าช่วงปกติแล้ว (ค่าปัจจุบัน: %.2f%s)", machine.GetTypeLabel(), temp, unit)
			log.Printf("✅ NORMAL: %s Probe %d - %.2f%s returned to normal range",
				machine.MachineName, probeNo, temp, unit)

			// Note: temp_log is already created in pollAndSave()
			// No need to insert again here to avoid duplicate key error

			// ส่ง Alert API notification ว่ากลับปกติ
			if p.apiNotificationService.IsLegacyAPIEnabled() {
				// Get McuID from latest probe data
				response := tcpclient.RequestFromTCPServer(
					tcpclient.ServerConfig{
						IP:   machine.MachineIP,
						Port: defaultTCPPort,
						Name: machine.MachineName,
					},
					"A",
					3*time.Second,
				)
				mcuID := machine.MachineName
				for _, pd := range response.Probes {
					if pd.ProbeNo == probeNo {
						mcuID = fmt.Sprintf("%s(%d)", machine.MachineName, pd.ProbeNo)
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
					MachineName: machine.MachineName,
					ProbeNo:     probeNo,
					MinTemp:     minTemp,
					MaxTemp:     maxTemp,
				}
				go func(pl AlertPayload) {
					if err := p.apiNotificationService.SendAlert(pl); err != nil {
						utils.LogError("checkProbeAlert - Failed to send recovery notification (machine=%s, probe=%d): %v", pl.MachineName, pl.ProbeNo, err)
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
