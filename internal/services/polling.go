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

var defaultTCPPort = 8899

func init() {
	if portStr := os.Getenv("DEFAULT_TCP_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			defaultTCPPort = port
		}
	}
}

type DataSavedEvent struct {
	Saved  int `json:"saved"`
	Errors int `json:"errors"`
}

const (
	MaxSensorTemp = 80.0 // legacy fallback, not used directly

	MinTempValue = -100.0
	MaxTempValue = 50.0
	MinHumiValue = 0.0
	MaxHumiValue = 100.0
)

func isValidSensorValue(value float64, sType string) bool {
	switch sType {
	case "h":
		return value >= MinHumiValue && value <= MaxHumiValue
	default: // "t" or ""
		return value >= MinTempValue && value <= MaxTempValue
	}
}

type TemperatureUpdateEvent struct {
	MachineName string  `json:"machineName"`
	TempValue   float64 `json:"tempValue"`
	Status      string  `json:"status"`
	MachineType string  `json:"type"`
	Timestamp   string  `json:"timestamp"`
	MinTemp     float64 `json:"minTemp"`
	MaxTemp     float64 `json:"maxTemp"`
	IPAddress   string  `json:"ipAddress"`
	ProbeNo     int     `json:"probeNo"`
}

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
	alertStates            map[string]string
	alertStatesMu          sync.Mutex
	machineCache           []models.MasterMachine
	machineCacheTime       time.Time
	machineCacheMu         sync.Mutex
}

func NewPollingService() *PollingService {
	return &PollingService{
		pollInterval:           5 * time.Minute,
		alertInterval:          5 * time.Second,
		stopChan:               make(chan struct{}),
		subscribers:            make([]chan DataSavedEvent, 0),
		temperatureSubscribers: make([]chan []TemperatureUpdateEvent, 0),
		apiNotificationService: NewAPINotificationService(),
		mqttService:            GlobalMQTTService,
		alertStates:            make(map[string]string),
	}
}

func (p *PollingService) Subscribe() chan DataSavedEvent {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	ch := make(chan DataSavedEvent, 10)
	p.subscribers = append(p.subscribers, ch)
	return ch
}

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

func (p *PollingService) SubscribeTemperature() chan []TemperatureUpdateEvent {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	ch := make(chan []TemperatureUpdateEvent, 10)
	p.temperatureSubscribers = append(p.temperatureSubscribers, ch)
	return ch
}

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

func (p *PollingService) Start() {
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
	p.stopChan = make(chan struct{})
	p.mu.Unlock()

	log.Println("Starting background polling service...")
	log.Printf("- Poll & Save interval: every %v", p.pollInterval)
	log.Printf("- Alert check interval: every %v", p.alertInterval)

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

	testTime := database.GetThailandTime()
	log.Printf("Timezone test: %v", testTime.Format("2006-01-02 15:04:05.000 MST"))

	if sqlDB, err := database.DB.DB(); err == nil {
		if err := sqlDB.Ping(); err != nil {
			log.Printf("Database ping failed: %v", err)
		} else {
			log.Println("Database connection verified")
		}
	}

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

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(p.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							utils.LogError("PANIC in poll ticker iteration: %v", r)
							log.Printf("PANIC in poll ticker iteration: %v (will retry next tick)", r)
						}
					}()
					p.pollAndSave()
				}()
			case <-p.stopChan:
				return
			}
		}
	}()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(p.alertInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							utils.LogError("PANIC in alert checker iteration: %v", r)
							log.Printf("PANIC in alert checker iteration: %v (will retry next tick)", r)
						}
					}()
					p.checkAlerts()
				}()
			case <-p.stopChan:
				return
			}
		}
	}()
}

func (p *PollingService) Stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	stopChan := p.stopChan
	p.mu.Unlock()

	close(stopChan)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("Polling service stopped gracefully")
	case <-time.After(10 * time.Second):
		log.Println("Polling service stop timed out after 10s, forcing shutdown")
	}
}

// TriggerOnce runs one poll-and-save cycle immediately without affecting the regular interval.
func (p *PollingService) TriggerOnce() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				utils.LogError("PANIC in manual poll: %v", r)
				log.Printf("PANIC in manual poll: %v", r)
			}
		}()
		p.pollAndSave()
	}()
}

// getMachines returns machines from a 1-minute cache, falling back to a fresh DB query.
// The lock is NOT held during the DB query so that pollAndSave() cache updates are
// never blocked by a slow query inside checkAlerts().
func (p *PollingService) getMachines() ([]models.MasterMachine, error) {
	p.machineCacheMu.Lock()
	if time.Since(p.machineCacheTime) < time.Minute && len(p.machineCache) > 0 {
		result := p.machineCache
		p.machineCacheMu.Unlock()
		return result, nil
	}
	stale := p.machineCache // keep reference in case DB fails
	p.machineCacheMu.Unlock()

	// DB query happens outside the lock.
	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		if len(stale) > 0 {
			log.Printf("getMachines - DB error, serving stale cache: %v", err)
			return stale, nil
		}
		return nil, err
	}

	p.machineCacheMu.Lock()
	p.machineCache = machines
	p.machineCacheTime = time.Now()
	p.machineCacheMu.Unlock()
	return machines, nil
}

func (p *PollingService) pollAndSave() {
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

	if sqlDB, err := database.DB.DB(); err == nil {
		if err := sqlDB.Ping(); err != nil {
			log.Printf("Database ping failed in pollAndSave: %v, will retry next cycle", err)
			return
		}
	}

	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		utils.LogError("pollAndSave - Failed to load machines: %v", err)
		log.Printf("Error loading machines: %v", err)
		log.Println("This might be a charset encoding issue")
		log.Println("Check if DB_CHARSET in .env matches your database charset")
		return
	}

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
		machineName := probes[0].MachineName

		response := tcpclient.RequestFromTCPServer(
			tcpclient.ServerConfig{IP: ip, Port: defaultTCPPort, Name: machineName},
			"A",
			5*time.Second,
		)

		probeConfigs := make(map[int]models.MasterMachine)
		for _, probe := range probes {
			probeConfigs[probe.ProbeNo] = probe
		}

		for _, probeData := range response.Probes {
			if probeData.RealValue == 65535 || probeData.RealValue == -1 {
				log.Printf("Skipping broken sensor data: %s Probe %d (RealValue: 0x%04X)", probes[0].MachineName, probeData.ProbeNo, uint16(probeData.RealValue))
				continue
			}

			probeConfig, hasConfig := probeConfigs[probeData.ProbeNo]
			if !hasConfig {
				probeConfig = probes[0]
				probeConfig.ProbeNo = probeData.ProbeNo
			}
			if probeConfig.SType == "" {
				probeConfig.SType = "t"
			}

			adjustedTemp := math.Round((probeData.TempValue+probeConfig.GetAdjTemp())*100) / 100

			if !isValidSensorValue(adjustedTemp, probeConfig.SType) {
				log.Printf("Skipping out-of-range value: %s Probe %d sType=%s value=%.2f",
					probeConfig.MachineName, probeData.ProbeNo, probeConfig.SType, adjustedTemp)
				continue
			}

			tempStatus := "N"
			if adjustedTemp < probeConfig.GetMinTemp() {
				tempStatus = "L"
			} else if adjustedTemp > probeConfig.GetMaxTemp() {
				tempStatus = "H"
			}

			realValueInt := probeData.RealValue
			insertTime := database.GetThailandTime().Truncate(time.Microsecond)

			log.Printf("InsertTime for %s Probe %d: %v", machineName, probeData.ProbeNo, insertTime)

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

			if err := database.DB.Create(&tempLog).Error; err != nil {
				if strings.Contains(err.Error(), "Duplicate entry") || strings.Contains(err.Error(), "1062") {
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

				if p.apiNotificationService.IsLegacyAPIEnabled() {
					payload := TempLogPayload{
						McuID:     probeConfig.MachineName,
						Status:    "00000110",
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

			p.checkProbeAlert(probeConfig, probeData.ProbeNo, adjustedTemp)
		}
	}

	// Warm the cache with the freshly-polled machine list so that checkAlerts()
	// can skip its own DB query until the next minute.
	p.machineCacheMu.Lock()
	p.machineCache = machines
	p.machineCacheTime = time.Now()
	p.machineCacheMu.Unlock()

	elapsed := time.Since(startTime)
	log.Printf("=== Poll & Save completed in %v ===", elapsed)
	log.Printf("   Saved: %d logs, %d errors", savedCount, errorCount)

	p.notifySubscribers(DataSavedEvent{Saved: savedCount, Errors: errorCount})
}

func (p *PollingService) checkAlerts() {
	if sqlDB, err := database.DB.DB(); err == nil {
		if err := sqlDB.Ping(); err != nil {
			log.Printf("Database ping failed in checkAlerts: %v", err)
			return
		}
	}

	machines, err := p.getMachines()
	if err != nil {
		log.Printf("checkAlerts - Failed to load machines: %v", err)
		return
	}

	machinesByIP := make(map[string][]models.MasterMachine)
	for _, m := range machines {
		machinesByIP[m.MachineIP] = append(machinesByIP[m.MachineIP], m)
	}

	var mqttPayloads []MQTTTemperaturePayload
	now := database.GetThailandTime()

	for ip, probes := range machinesByIP {
		machineName := probes[0].MachineName

		response := tcpclient.RequestFromTCPServer(
			tcpclient.ServerConfig{IP: ip, Port: defaultTCPPort, Name: machineName},
			"A",
			3*time.Second,
		)

		probeConfigs := make(map[int]models.MasterMachine)
		for _, probe := range probes {
			probeConfigs[probe.ProbeNo] = probe
		}

		for _, probeData := range response.Probes {
			if probeData.RealValue == 65535 || probeData.RealValue == -1 {
				continue
			}

			probeConfig, hasConfig := probeConfigs[probeData.ProbeNo]
			if !hasConfig {
				probeConfig = probes[0]
				probeConfig.ProbeNo = probeData.ProbeNo
			}

			adjustedTemp := math.Round((probeData.TempValue+probeConfig.GetAdjTemp())*100) / 100

			if !isValidSensorValue(adjustedTemp, probeConfig.SType) {
				log.Printf("Skipping out-of-range value: %s Probe %d sType=%s value=%.2f",
					probeConfig.MachineName, probeData.ProbeNo, probeConfig.SType, adjustedTemp)
				continue
			}

			p.checkProbeAlert(probeConfig, probeData.ProbeNo, adjustedTemp)

			tempStatus := "N"
			if adjustedTemp < probeConfig.GetMinTemp() {
				tempStatus = "L"
			} else if adjustedTemp > probeConfig.GetMaxTemp() {
				tempStatus = "H"
			}

			mqttPayloads = append(mqttPayloads, MQTTTemperaturePayload{
				Probe:       probeConfig.MachineName,
				Temp:        adjustedTemp,
				Status:      tempStatus,
				MachineType: probeConfig.SType,
				Timestamp:   now.Format("2006-01-02 15:04:05"),
				MinTemp:     probeConfig.GetMinTemp(),
				MaxTemp:     probeConfig.GetMaxTemp(),
				IPAddress:   probeConfig.MachineIP,
				ProbeNo:     probeData.ProbeNo,
			})
		}
	}

	if len(mqttPayloads) == 0 {
		return
	}

	if p.mqttService == nil {
		log.Println("MQTT service is nil - skipping publish")
	} else if !p.mqttService.IsEnabled() {
		// MQTT disabled — expected if not configured
	} else if !p.mqttService.IsConnected() {
		log.Println("MQTT not connected - skipping publish")
	} else {
		go func(payloads []MQTTTemperaturePayload) {
			if err := p.mqttService.PublishTemperatureBatch(payloads); err != nil {
				utils.LogError("MQTT batch publish failed: %v", err)
				log.Printf("MQTT publish error: %v", err)
			} else {
				log.Printf("MQTT published %d temperature readings", len(payloads))
			}
		}(mqttPayloads)
	}
	// Build SSE events — use the status already computed above, not a hardcoded "N".
	sseEvents := make([]TemperatureUpdateEvent, 0, len(mqttPayloads))
	for _, payload := range mqttPayloads {
		sseEvents = append(sseEvents, TemperatureUpdateEvent{
			MachineName: payload.Probe,
			TempValue:   payload.Temp,
			Status:      payload.Status,
			MachineType: payload.MachineType,
			Timestamp:   payload.Timestamp,
			MinTemp:     payload.MinTemp,
			MaxTemp:     payload.MaxTemp,
			IPAddress:   payload.IPAddress,
			ProbeNo:     payload.ProbeNo,
		})
	}
	p.notifyTemperatureSubscribers(sseEvents)
}

func (p *PollingService) sendAlertNotification(payload AlertPayload) {
	if !p.apiNotificationService.IsLegacyAPIEnabled() {
		return
	}
	go func(pl AlertPayload) {
		if err := p.apiNotificationService.SendAlert(pl); err != nil {
			utils.LogError("sendAlertNotification - Failed to send alert (machine=%s, probe=%d, type=%s): %v", pl.MachineName, pl.ProbeNo, pl.AlertType, err)
			log.Printf("Failed to send alert notification: %v", err)
		} else {
			log.Printf("Alert notification sent for %s Probe %d [%s]", pl.MachineName, pl.ProbeNo, pl.AlertType)
		}
	}(payload)
}

func (p *PollingService) checkProbeAlert(machine models.MasterMachine, probeNo int, temp float64) {
	alertKey := fmt.Sprintf("%s:%d", machine.MachineIP, probeNo)

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

	// Claim the state transition atomically before any side-effecting work.
	// This prevents the poll goroutine and the alert-check goroutine from both
	// sending duplicate notifications for the same transition.
	p.alertStatesMu.Lock()
	prevState := p.alertStates[alertKey]
	if currentState == prevState {
		p.alertStatesMu.Unlock()
		return
	}
	p.alertStates[alertKey] = currentState
	p.alertStatesMu.Unlock()

	now := database.GetThailandTime().Truncate(time.Microsecond)
	dateStr := now.Format("20060102")
	timeStr := now.Format("15:04:05")

	if currentState == "H" || currentState == "L" {
		alertTypeStr := "HIGH"
		if currentState == "L" {
			alertTypeStr = "LOW"
		}

		unit := machine.GetUnit()
		typeLabel := machine.GetTypeLabel()
		alertMessage := fmt.Sprintf("%s %sเกิน (ค่าปัจจุบัน: %.2f%s, ช่วง: %.2f-%.2f%s) %s(%d) %s %s",
			typeLabel,
			map[string]string{"H": "สูง", "L": "ต่ำ"}[currentState],
			temp, unit, minTemp, maxTemp, unit, machine.MachineName, probeNo, now.Format("2006/01/02"), timeStr)

		log.Printf("ALERT: %s Probe %d - %s %.2f%s is %s (min: %.2f, max: %.2f)",
			machine.MachineName, probeNo, typeLabel, temp, unit, alertTypeStr, minTemp, maxTemp)

		tempError := models.TempError{
			MachineIP:   machine.MachineIP,
			ProbeNo:     probeNo,
			MachineName: &machine.MachineName,
			TempValue:   &temp,
			ErrorTime:   database.GetThailandTime().Truncate(time.Microsecond),
			MinTemp:     &minTemp,
			MaxTemp:     &maxTemp,
			TempStatus:  "p",
			ErrorType:   "o",
			SType:       machine.SType,
		}

		if err := database.DB.Create(&tempError).Error; err != nil {
			if !strings.Contains(err.Error(), "Duplicate entry") && !strings.Contains(err.Error(), "1062") {
				utils.LogError("checkAlerts - Failed to create temp_error: %v", err)
			}
		}

		p.sendAlertNotification(AlertPayload{
			McuID:       machine.MachineName,
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
		})
	}

	if currentState == "N" && (prevState == "H" || prevState == "L") {
		unit := machine.GetUnit()
		normalMessage := fmt.Sprintf("%s กลับเข้าช่วงปกติแล้ว (ค่าปัจจุบัน: %.2f%s) %s %s",
			machine.GetTypeLabel(), temp, unit, now.Format("2006/01/02"), timeStr)
		log.Printf("NORMAL: %s Probe %d - %.2f%s returned to normal range",
			machine.MachineName, probeNo, temp, unit)

		p.sendAlertNotification(AlertPayload{
			McuID:       machine.MachineName,
			Status:      "00000001",
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
		})
	}
}

func (p *PollingService) notifySubscribers(event DataSavedEvent) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for _, ch := range p.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (p *PollingService) notifyTemperatureSubscribers(events []TemperatureUpdateEvent) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for _, ch := range p.temperatureSubscribers {
		select {
		case ch <- events:
		default:
		}
	}
}

var GlobalPollingService *PollingService
