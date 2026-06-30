package tcpclient

import (
	"encoding/hex"
	"fmt"
	"log"
	"math"
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

	address := net.JoinHostPort(config.IP, fmt.Sprintf("%d", config.Port))

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
// - 1 probe:  41 41 5a 00 5a 19 a3 5a 0d (9 bytes)
// - 2 probes: 41 41 5a 03 5a 19 a3 5a 19 ae 5a 0d (12 bytes)
// OR
// - 2 probes: 41 41 5a [probe1_2bytes] 5a [probe2_2bytes] 5a 0d (depends on device)
func parseHexResponse(data []byte, ip string) []ProbeData {
	probes := []ProbeData{}
	hexStr := strings.ToUpper(hex.EncodeToString(data))
	log.Printf("Received hex data (%s): %s", ip, formatHexString(hexStr))
	log.Printf("Buffer length: %d bytes", len(data))

	// Check minimum length
	if len(data) < 9 {
		log.Printf("Buffer too short (expected at least 9 bytes)")
		return probes
	}

	// Verify header: 41 41 5a
	if data[0] != 0x41 || data[1] != 0x41 {
		log.Printf("Invalid header, expected 41 41 got %02X %02X", data[0], data[1])
		return probes
	}

	log.Printf("Valid header: 41 41")

	// Check probe indicator at index 3
	probeIndicator := data[3]
	log.Printf("Probe indicator at index [3]: 0x%02X", probeIndicator)

	// Determine number of probes based on buffer length and format
	// Format 1: 41 41 5a 00/03 5a [2bytes] [5a [2bytes]] 5a 0d
	// Format 2: 41 41 5a ?? 5a [temp1_2b] 5a [hum1_2b] 5a [temp2_2b] 5a 0d (15 bytes)
	var hasProbe2 bool
	var is15Byte bool
	var is18Byte bool

	switch len(data) {
	case 18:
		is18Byte = true
		log.Printf("Detected 18 bytes → temp probe1 + humidity probe1 + temp probe2 + humidity probe2")
	case 15:
		is15Byte = true
		log.Printf("Detected 15 bytes → temp probe1 + humidity probe1 + temp probe2")
	case 12:
		hasProbe2 = true
		log.Printf("Detected 12 bytes → expecting 2 probes")
	case 9:
		hasProbe2 = false
		log.Printf("Detected 9 bytes → expecting 1 probe")
	default:
		if probeIndicator == 0x03 {
			hasProbe2 = true
			log.Printf("Probe indicator 0x03 → expecting 2 probes")
		}
	}

	// Parse Probe 1 - Temperature (index 5, 6)
	var probe1Temp float64
	if len(data) >= 7 && data[4] == 0x5a {
		probe1Value := int(data[5])<<8 | int(data[6])
		probe1Temp = float64(probe1Value-4000) * 0.01
		log.Printf("Probe 1: bytes[5,6]=0x%02X%02X, decimal=%d, temp=%.2f°C",
			data[5], data[6], probe1Value, probe1Temp)

		probes = append(probes, ProbeData{
			ProbeNo:   1,
			McuID:     "A",
			TempValue: roundTo2Decimal(probe1Temp),
			RealValue: probe1Value,
			Status:    "00",
		})
	} else {
		log.Printf("Probe 1: invalid separator at index [4], expected 0x5A, got 0x%02X", data[4])
	}

	if is18Byte {
		// 18-byte format: bytes[5,6]=temp1 | bytes[8,9]=humidity1 | bytes[11,12]=temp2 | bytes[14,15]=humidity2

		// Parse Humidity Probe 1 (index 8, 9)
		if len(data) >= 10 && data[7] == 0x5a {
			humRaw := int(data[8])<<8 | int(data[9])
			humValue := calcHumidity(humRaw, probe1Temp, 0)
			log.Printf("Humidity 1: bytes[8,9]=0x%02X%02X, raw=%d, rh=%.2f%%",
				data[8], data[9], humRaw, humValue)
			probes = append(probes, ProbeData{
				ProbeNo:   2,
				McuID:     "h",
				TempValue: humValue,
				RealValue: humRaw,
				Status:    "00",
			})
		} else {
			log.Printf("Humidity 1: invalid separator at index [7], expected 0x5A, got 0x%02X", data[7])
		}

		// Parse Temperature Probe 2 (index 11, 12)
		var probe2Temp float64
		if len(data) >= 13 && data[10] == 0x5a {
			probe2Value := int(data[11])<<8 | int(data[12])
			probe2Temp = float64(probe2Value-4000) * 0.01
			log.Printf("Probe 2: bytes[11,12]=0x%02X%02X, decimal=%d, temp=%.2f°C",
				data[11], data[12], probe2Value, probe2Temp)
			probes = append(probes, ProbeData{
				ProbeNo:   3,
				McuID:     "A",
				TempValue: roundTo2Decimal(probe2Temp),
				RealValue: probe2Value,
				Status:    "00",
			})
		} else {
			log.Printf("Probe 2: invalid separator at index [10], expected 0x5A, got 0x%02X", data[10])
		}

		// Parse Humidity Probe 2 (index 14, 15)
		if len(data) >= 16 && data[13] == 0x5a {
			hum2Raw := int(data[14])<<8 | int(data[15])
			hum2Value := calcHumidity(hum2Raw, probe2Temp, 0)
			log.Printf("Humidity 2: bytes[14,15]=0x%02X%02X, raw=%d, rh=%.2f%%",
				data[14], data[15], hum2Raw, hum2Value)
			probes = append(probes, ProbeData{
				ProbeNo:   4,
				McuID:     "h",
				TempValue: hum2Value,
				RealValue: hum2Raw,
				Status:    "00",
			})
		} else {
			log.Printf("Humidity 2: invalid separator at index [13], expected 0x5A, got 0x%02X", data[13])
		}
	} else if is15Byte {
		// 15-byte format: bytes[5,6]=temp1 | bytes[8,9]=humidity1 | bytes[11,12]=temp2

		// Parse Humidity Probe 1 (index 8, 9)
		if len(data) >= 10 && data[7] == 0x5a {
			humRaw := int(data[8])<<8 | int(data[9])
			humValue := calcHumidity(humRaw, probe1Temp, 0)
			log.Printf("Humidity 1: bytes[8,9]=0x%02X%02X, raw=%d, rh=%.2f%%",
				data[8], data[9], humRaw, humValue)

			probes = append(probes, ProbeData{
				ProbeNo:   2,
				McuID:     "h",
				TempValue: humValue,
				RealValue: humRaw,
				Status:    "00",
			})
		} else {
			log.Printf("Humidity 1: invalid separator at index [7], expected 0x5A, got 0x%02X", data[7])
		}

		// Parse Temperature Probe 2 (index 11, 12)
		if len(data) >= 13 && data[10] == 0x5a {
			probe2Value := int(data[11])<<8 | int(data[12])
			probe2Temp := float64(probe2Value-4000) * 0.01
			log.Printf("Probe 2: bytes[11,12]=0x%02X%02X, decimal=%d, temp=%.2f°C",
				data[11], data[12], probe2Value, probe2Temp)

			probes = append(probes, ProbeData{
				ProbeNo:   3,
				McuID:     "A",
				TempValue: roundTo2Decimal(probe2Temp),
				RealValue: probe2Value,
				Status:    "00",
			})
		} else {
			log.Printf("Probe 2: invalid separator at index [10], expected 0x5A, got 0x%02X", data[10])
		}
	} else if hasProbe2 && len(data) >= 10 {
		// Parse Probe 2 (index 8, 9) if exists
		if data[7] == 0x5a {
			probe2Value := int(data[8])<<8 | int(data[9])
			probe2Temp := float64(probe2Value-4000) * 0.01
			log.Printf("Probe 2: bytes[8,9]=0x%02X%02X, decimal=%d, temp=%.2f°C",
				data[8], data[9], probe2Value, probe2Temp)

			probes = append(probes, ProbeData{
				ProbeNo:   2,
				McuID:     "B",
				TempValue: roundTo2Decimal(probe2Temp),
				RealValue: probe2Value,
				Status:    "00",
			})
		} else {
			log.Printf("Probe 2: invalid separator at index [7], expected 0x5A, got 0x%02X", data[7])
			log.Printf("Full data dump:")
			for i, b := range data {
				log.Printf("   [%d] = 0x%02X (%d)", i, b, b)
			}
		}
	}

	log.Printf("Successfully parsed %d probe(s)", len(probes))
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
	return math.Round(val*100) / 100
}

// calcHumidity calculates relative humidity using Sensirion SHT1x temperature compensation formula.
// rhRaw: raw integer value from humidity sensor (bytes 8,9 in 15-byte protocol)
// temp:  temperature in °C from the paired temperature probe (CalTemp of bytes 5,6)
// aVal:  adjustment offset (0 = no adjustment)
// Formula: RH = (-4 + 0.0405*r - 0.0000028*r²) + (T-25)*(0.01 + 0.00008*r) + aVal
func calcHumidity(rhRaw int, temp float64, aVal float64) float64 {
	r := float64(rhRaw)
	part1 := -4 + (0.0405 * r) + (-0.0000028 * r * r)
	part2 := (temp - 25) * (0.01 + (0.00008 * r))
	rh := part1 + part2 + aVal
	return math.Round(rh*100) / 100
}
