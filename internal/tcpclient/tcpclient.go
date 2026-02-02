package tcpclient

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// ProbeData represents temperature probe data
type ProbeData struct {
	ProbeNo   int     `json:"probeNo"`
	McuID     string  `json:"mcuId"`
	TempValue float64 `json:"tempValue"`
	RealValue int     `json:"realValue"`
	Status    string  `json:"status"`
}

// ServerResponse represents TCP server response
type ServerResponse struct {
	IP        string      `json:"ip"`
	Port      int         `json:"port"`
	Connected bool        `json:"connected"`
	Data      string      `json:"data"`
	Error     string      `json:"error"`
	Timestamp time.Time   `json:"timestamp"`
	Probes    []ProbeData `json:"probes"`
}

// ServerConfig represents TCP server configuration
type ServerConfig struct {
	IP   string
	Port int
	Name string
}

// RequestFromTCPServer connects to a TCP server and requests data
func RequestFromTCPServer(config ServerConfig, command string, timeout time.Duration) ServerResponse {
	result := ServerResponse{
		IP:        config.IP,
		Port:      config.Port,
		Connected: false,
		Timestamp: time.Now(),
		Probes:    []ProbeData{},
	}

	address := fmt.Sprintf("%s:%d", config.IP, config.Port)

	// Connect with timeout
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		result.Error = fmt.Sprintf("Connection failed: %v", err)
		log.Printf("TCP %s: %s", config.IP, result.Error)
		return result
	}
	defer conn.Close()

	result.Connected = true

	// Set read/write deadline
	conn.SetDeadline(time.Now().Add(timeout))

	// Send command
	if command == "" {
		command = "A"
	}
	_, err = conn.Write([]byte(command + "\r"))
	if err != nil {
		result.Error = fmt.Sprintf("Write failed: %v", err)
		log.Printf("TCP %s: %s", config.IP, result.Error)
		return result
	}

	// Read response
	buffer := make([]byte, 1024)
	var dataBuffer []byte

	for {
		n, err := conn.Read(buffer)
		if err != nil {
			// Timeout or EOF is expected
			break
		}
		dataBuffer = append(dataBuffer, buffer[:n]...)

		// Check for end marker (0x0D)
		if len(dataBuffer) > 0 && dataBuffer[len(dataBuffer)-1] == 0x0D {
			break
		}
	}

	if len(dataBuffer) > 0 {
		result.Data = hex.EncodeToString(dataBuffer)
		result.Probes = parseHexResponse(dataBuffer, config.IP)
		log.Printf("TCP %s: Parsed %d probes", config.IP, len(result.Probes))
	}

	return result
}

// parseHexResponse parses hex response from temperature sensor
// Protocol format:
// - 1 probe:  41 41 5a 00 5a 1a b6 5a 0d (9 bytes)
// - 2 probes: 41 41 5a 03 5a 19 a3 5a 19 ae 5a 0d (12 bytes)
func parseHexResponse(data []byte, ip string) []ProbeData {
	probes := []ProbeData{}
	hexStr := strings.ToUpper(hex.EncodeToString(data))
	log.Printf("Received hex data: %s", formatHexString(hexStr))
	log.Printf("Buffer length: %d", len(data))

	// Check minimum length
	if len(data) < 9 {
		log.Printf("Buffer too short for expected format")
		return probes
	}

	// Verify header: 41 41 5a
	if data[0] != 0x41 || data[1] != 0x41 || data[2] != 0x5a {
		log.Printf("Invalid header, expected 41 41 5a")
		return probes
	}

	// Check probe indicator at index 3
	probeIndicator := data[3]
	hasProbe2 := probeIndicator == 0x03

	// Parse Probe 1 (index 5, 6)
	if len(data) >= 7 && data[4] == 0x5a {
		probe1Value := int(data[5])<<8 | int(data[6])
		probe1Temp := float64(probe1Value-4000) * 0.01
		log.Printf("Probe 1: hex=%04X, decimal=%d, temp=%.2f°C", probe1Value, probe1Value, probe1Temp)

		probes = append(probes, ProbeData{
			ProbeNo:   1,
			McuID:     "A",
			TempValue: roundTo2Decimal(probe1Temp),
			RealValue: probe1Value,
			Status:    "00",
		})
	}

	// Parse Probe 2 (index 8, 9) if exists
	if hasProbe2 && len(data) >= 10 && data[7] == 0x5a {
		probe2Value := int(data[8])<<8 | int(data[9])
		probe2Temp := float64(probe2Value-4000) * 0.01
		log.Printf("Probe 2: hex=%04X, decimal=%d, temp=%.2f°C", probe2Value, probe2Value, probe2Temp)

		probes = append(probes, ProbeData{
			ProbeNo:   2,
			McuID:     "B",
			TempValue: roundTo2Decimal(probe2Temp),
			RealValue: probe2Value,
			Status:    "00",
		})
	}

	return probes
}

func formatHexString(s string) string {
	var result []string
	for i := 0; i < len(s); i += 2 {
		if i+2 <= len(s) {
			result = append(result, s[i:i+2])
		}
	}
	return strings.Join(result, " ")
}

func roundTo2Decimal(val float64) float64 {
	return float64(int(val*100+0.5)) / 100
}
