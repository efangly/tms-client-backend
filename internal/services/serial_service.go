package services

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"tms-backend/internal/database"
	"tms-backend/internal/models"
	"tms-backend/internal/serialclient"
)

var defaultSerialBaud = 9600
var defaultSerialBootDelaySeconds = 2

func init() {
	if baudStr := os.Getenv("DEFAULT_SERIAL_BAUD"); baudStr != "" {
		if baud, err := strconv.Atoi(baudStr); err == nil {
			defaultSerialBaud = baud
		}
	}
	if delayStr := os.Getenv("DEFAULT_SERIAL_BOOT_DELAY"); delayStr != "" {
		if delay, err := strconv.Atoi(delayStr); err == nil {
			defaultSerialBootDelaySeconds = delay
		}
	}
}

// DefaultSerialBaud returns the baud rate the serial service opens every
// COM port with. Exposed for the same reason as polling.DefaultTCPPort: env
// vars are only applied here at package init time.
func DefaultSerialBaud() int {
	return defaultSerialBaud
}

// DefaultSerialBootDelay returns how long the serial service waits after
// opening a COM port before treating it as usable. Some USB-to-serial
// adapters reset the attached MCU on open (DTR/RTS assert) and need a
// moment to boot before they'll respond — confirmed against real hardware
// (an FTDI FT232 adapter) needing ~2s. Configurable via
// DEFAULT_SERIAL_BOOT_DELAY (seconds) since different adapters/devices may
// need more or less.
func DefaultSerialBootDelay() time.Duration {
	return time.Duration(defaultSerialBootDelaySeconds) * time.Second
}

// SerialService keeps a persistent, auto-reconnecting connection open for
// every COM-attached probe (machine_ip values starting with "COM"), so
// polling can reuse an already-open port instead of dialing per request.
type SerialService struct {
	manager *serialclient.Manager
}

// NewSerialService creates a SerialService with no open connections yet.
func NewSerialService() *SerialService {
	return &SerialService{manager: serialclient.NewManager()}
}

// Start loads all configured machines, opens a persistent connection for
// each distinct COM-prefixed machine_ip, and leaves auto-reconnect enabled
// so a port that isn't available yet (or drops later) keeps retrying in the
// background instead of failing startup.
func (s *SerialService) Start() error {
	var machines []models.MasterMachine
	if err := database.DB.Find(&machines).Error; err != nil {
		return fmt.Errorf("serial service: query machines: %w", err)
	}

	seen := make(map[string]bool)
	total := 0
	opened := 0

	for _, m := range machines {
		if !serialclient.IsSerialPath(m.MachineIP) || seen[m.MachineIP] {
			continue
		}
		seen[m.MachineIP] = true
		total++

		conn := s.manager.Open(serialclient.PortConfig{
			Path:            m.MachineIP,
			BaudRate:        defaultSerialBaud,
			AutoReconnect:   true,
			BootSettleDelay: DefaultSerialBootDelay(),
		})
		if err := conn.Connect(); err != nil {
			log.Printf("Serial %s: initial connect failed, will keep retrying via auto-reconnect: %v", m.MachineIP, err)
			continue
		}
		opened++
	}

	log.Printf("Serial service: opened %d/%d configured COM port(s)", opened, total)
	return nil
}

// GetConnection returns the persistent connection for a COM path, if one is
// being tracked (i.e. it was present in master_machine when Start ran).
func (s *SerialService) GetConnection(path string) (*serialclient.Connection, bool) {
	return s.manager.Get(path)
}

// Stop disconnects every open serial port.
func (s *SerialService) Stop() {
	s.manager.StopAll()
}

var GlobalSerialService *SerialService
