// Package serialclient manages persistent USB serial port connections to
// probe devices, mirroring the TCP client in internal/tcpclient but keeping
// a single long-lived connection per port instead of dialing per request.
package serialclient

import (
	"fmt"
	"log"
	"sync"
	"time"

	"go.bug.st/serial"
)

const defaultReconnectInterval = 5 * time.Second

// defaultBootSettleDelay is how long to wait after successfully opening a
// port before treating it as usable, when PortConfig.BootSettleDelay is
// zero. Confirmed against real hardware: a USB-to-serial adapter (FTDI
// FT232) resets the attached MCU on DTR/RTS assert when the port opens, and
// the device doesn't respond to the first command sent immediately
// afterward — it needs a moment to reboot.
const defaultBootSettleDelay = 2 * time.Second

// PortConfig describes how to open and maintain a single serial port.
type PortConfig struct {
	Path              string // e.g. "COM3"
	BaudRate          int
	AutoReconnect     bool
	ReconnectInterval time.Duration // defaults to 5s when zero
	BootSettleDelay   time.Duration // defaults to 2s when zero; see defaultBootSettleDelay
}

// Connection wraps a single persistent serial port. It is safe for
// concurrent use: Connect/Disconnect/IsConnected all take the same lock.
type Connection struct {
	mu             sync.Mutex
	config         PortConfig
	port           serial.Port
	connected      bool
	stopped        bool
	reconnectTimer *time.Timer
}

// NewConnection creates a Connection for the given config. Connect must be
// called explicitly to open the port.
func NewConnection(config PortConfig) *Connection {
	if config.ReconnectInterval <= 0 {
		config.ReconnectInterval = defaultReconnectInterval
	}
	if config.BootSettleDelay <= 0 {
		config.BootSettleDelay = defaultBootSettleDelay
	}
	return &Connection{config: config}
}

// Connect opens the serial port. If already connected, it is a no-op
// (mirrors the double-connect guard in the reference NestJS implementation).
// If the open fails and AutoReconnect is enabled, a reconnect is scheduled
// automatically — this covers a device that isn't plugged in yet at app
// startup, so it connects as soon as it becomes available rather than only
// reconnecting after a previously-successful connection drops.
func (c *Connection) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.connectLocked()
	if err != nil && c.config.AutoReconnect && !c.stopped {
		c.scheduleReconnectLocked()
	}
	return err
}

func (c *Connection) connectLocked() error {
	if c.connected && c.port != nil {
		log.Printf("Serial %s: already connected, skipping connect", c.config.Path)
		return nil
	}

	mode := &serial.Mode{
		BaudRate: c.config.BaudRate,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}

	port, err := serial.Open(c.config.Path, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", c.config.Path, err)
	}

	log.Printf("Serial %s: opened, waiting %v for device boot...", c.config.Path, c.config.BootSettleDelay)
	time.Sleep(c.config.BootSettleDelay)

	c.port = port
	c.connected = true
	log.Printf("Serial %s: connected (baud=%d)", c.config.Path, c.config.BaudRate)
	return nil
}

// Disconnect cancels any pending reconnect attempt and closes the port.
func (c *Connection) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	c.cancelReconnectLocked()
	c.closeLocked()
}

func (c *Connection) closeLocked() {
	if c.port != nil {
		if err := c.port.Close(); err != nil {
			log.Printf("Serial %s: close error: %v", c.config.Path, err)
		}
		c.port = nil
	}
	c.connected = false
}

// IsConnected reports whether the port is currently believed to be open.
func (c *Connection) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// handleIOError marks the connection as broken after a failed read/write and,
// if AutoReconnect is enabled, schedules a reconnect attempt. Unlike the
// Node `serialport` library, go.bug.st/serial has no async 'close' event, so
// a failed read/write is the only signal we get that the port dropped.
func (c *Connection) handleIOError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Printf("Serial %s: I/O error, marking disconnected: %v", c.config.Path, err)
	c.closeLocked()

	if c.config.AutoReconnect && !c.stopped {
		c.scheduleReconnectLocked()
	}
}

// scheduleReconnectLocked arms a single-shot timer to retry Connect after
// ReconnectInterval. Guarded by reconnectTimer so repeated failures never
// stack more than one pending attempt.
func (c *Connection) scheduleReconnectLocked() {
	if c.reconnectTimer != nil {
		return
	}
	c.reconnectTimer = time.AfterFunc(c.config.ReconnectInterval, func() {
		c.mu.Lock()
		c.reconnectTimer = nil
		if c.stopped {
			c.mu.Unlock()
			return
		}
		err := c.connectLocked()
		c.mu.Unlock()

		if err != nil {
			log.Printf("Serial %s: reconnect failed: %v", c.config.Path, err)
			c.mu.Lock()
			if c.config.AutoReconnect && !c.stopped {
				c.scheduleReconnectLocked()
			}
			c.mu.Unlock()
		}
	})
}

func (c *Connection) cancelReconnectLocked() {
	if c.reconnectTimer != nil {
		c.reconnectTimer.Stop()
		c.reconnectTimer = nil
	}
}
