package serialclient

import (
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"tms-backend/internal/tcpclient"
)

// maxRequestRetries mirrors tcpclient's retry count for anomalous readings,
// kept as its own constant since it's a per-transport tuning knob rather
// than something that should couple the two packages together.
const maxRequestRetries = 3

// Response mirrors tcpclient.ServerResponse's shape for a serial-attached
// probe, reusing tcpclient.ProbeData so downstream consumers (e.g. polling)
// can handle TCP and serial readings identically.
type Response struct {
	Path      string                `json:"path"`
	Connected bool                  `json:"connected"`
	Data      string                `json:"data"`
	Error     string                `json:"error"`
	Timestamp time.Time             `json:"timestamp"`
	Probes    []tcpclient.ProbeData `json:"probes"`
}

// sendOnce writes command+"\r" and reads back a single hex frame terminated
// by 0x0D, or until timeout elapses with no further data — the same framing
// rule as tcpclient's read loop, but over an already-open serial port
// instead of dialing a fresh TCP connection per request.
func (c *Connection) sendOnce(command string, timeout time.Duration) ([]byte, error) {
	c.mu.Lock()
	if !c.connected || c.port == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("serial %s: not connected", c.config.Path)
	}
	port := c.port
	c.mu.Unlock()

	if err := port.SetReadTimeout(timeout); err != nil {
		c.handleIOError(err)
		return nil, fmt.Errorf("serial %s: set read timeout: %w", c.config.Path, err)
	}

	if _, err := port.Write([]byte(command + "\r")); err != nil {
		c.handleIOError(err)
		return nil, fmt.Errorf("serial %s: write: %w", c.config.Path, err)
	}

	deadline := time.Now().Add(timeout)
	var buf []byte
	readBuf := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := port.Read(readBuf)
		if err != nil {
			c.handleIOError(err)
			return nil, fmt.Errorf("serial %s: read: %w", c.config.Path, err)
		}
		if n == 0 {
			// go.bug.st/serial returns (0, nil) when the read timeout
			// elapses without any data arriving.
			break
		}
		buf = append(buf, readBuf[:n]...)
		if buf[len(buf)-1] == 0x0D {
			break
		}
	}
	return buf, nil
}

// Request sends command (default "A") to the device and parses the hex
// response using tcpclient.ParseHexResponse — the identical protocol parser
// used for TCP-attached probes. As in tcpclient.RequestFromTCPServer, a
// reading that is physically implausible (e.g. a disconnected probe) is
// dropped and the request retried rather than forwarding the bad value.
func (c *Connection) Request(command string, timeout time.Duration) Response {
	result := Response{
		Path:      c.config.Path,
		Timestamp: time.Now(),
		Probes:    []tcpclient.ProbeData{},
	}

	if command == "" {
		command = "A"
	}

	var dataBuffer []byte
	var probes []tcpclient.ProbeData

	for attempt := 1; attempt <= maxRequestRetries; attempt++ {
		buf, err := c.sendOnce(command, timeout)
		if err != nil {
			result.Error = err.Error()
			log.Printf("Serial %s: %s", c.config.Path, result.Error)
			return result
		}
		result.Connected = true
		dataBuffer = buf

		parsed := tcpclient.ParseHexResponse(buf, c.config.Path)
		valid, hasAnomaly := tcpclient.FilterValidProbes(parsed, c.config.Path)
		probes = valid

		if !hasAnomaly || attempt == maxRequestRetries {
			break
		}

		log.Printf("Serial %s: Anomalous sensor data detected, retrying request (attempt %d/%d)",
			c.config.Path, attempt, maxRequestRetries)
	}

	if len(dataBuffer) > 0 {
		result.Data = hex.EncodeToString(dataBuffer)
		result.Probes = probes
		log.Printf("Serial %s: Parsed %d probes", c.config.Path, len(result.Probes))
	}

	return result
}
