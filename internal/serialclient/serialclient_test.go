package serialclient

import (
	"errors"
	"testing"
	"time"

	"go.bug.st/serial"
)

// fakePort is a minimal no-op implementation of serial.Port, used to put a
// Connection into a "connected" state without touching real hardware.
type fakePort struct{}

func (fakePort) SetMode(mode *serial.Mode) error                      { return nil }
func (fakePort) Read(p []byte) (int, error)                           { return 0, nil }
func (fakePort) Write(p []byte) (int, error)                          { return len(p), nil }
func (fakePort) Drain() error                                         { return nil }
func (fakePort) ResetInputBuffer() error                              { return nil }
func (fakePort) ResetOutputBuffer() error                             { return nil }
func (fakePort) SetDTR(dtr bool) error                                { return nil }
func (fakePort) SetRTS(rts bool) error                                { return nil }
func (fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) { return nil, nil }
func (fakePort) SetReadTimeout(t time.Duration) error                 { return nil }
func (fakePort) Close() error                                         { return nil }
func (fakePort) Break(time.Duration) error                            { return nil }

func TestConnect_InvalidPathReturnsError(t *testing.T) {
	c := NewConnection(PortConfig{Path: "COM_DOES_NOT_EXIST", BaudRate: 9600})

	if err := c.Connect(); err == nil {
		t.Fatal("expected error opening a nonexistent port, got nil")
	}
	if c.IsConnected() {
		t.Fatal("expected IsConnected() to be false after a failed Connect()")
	}
}

func TestConnect_DoubleConnectIsNoOp(t *testing.T) {
	c := NewConnection(PortConfig{Path: "COM_DOES_NOT_EXIST", BaudRate: 9600})

	// Force the connected state (with a fake open port) to exercise the
	// double-connect guard without needing real hardware.
	c.mu.Lock()
	c.connected = true
	c.port = fakePort{}
	c.mu.Unlock()

	if err := c.Connect(); err != nil {
		t.Fatalf("expected no-op success when already connected, got %v", err)
	}
}

func TestHandleIOError_SchedulesReconnectWhenEnabled(t *testing.T) {
	c := NewConnection(PortConfig{
		Path:              "COM_DOES_NOT_EXIST",
		BaudRate:          9600,
		AutoReconnect:     true,
		ReconnectInterval: time.Hour, // long enough that it won't fire during the test
	})

	c.handleIOError(errors.New("simulated I/O error"))

	c.mu.Lock()
	scheduled := c.reconnectTimer != nil
	connected := c.connected
	c.mu.Unlock()

	if connected {
		t.Fatal("expected connected=false after handleIOError")
	}
	if !scheduled {
		t.Fatal("expected a reconnect timer to be scheduled when AutoReconnect is true")
	}
}

func TestHandleIOError_NoReconnectWhenDisabled(t *testing.T) {
	c := NewConnection(PortConfig{Path: "COM_DOES_NOT_EXIST", BaudRate: 9600, AutoReconnect: false})

	c.handleIOError(errors.New("simulated I/O error"))

	c.mu.Lock()
	scheduled := c.reconnectTimer != nil
	c.mu.Unlock()

	if scheduled {
		t.Fatal("expected no reconnect timer when AutoReconnect is false")
	}
}

func TestDisconnect_CancelsPendingReconnect(t *testing.T) {
	c := NewConnection(PortConfig{
		Path:              "COM_DOES_NOT_EXIST",
		BaudRate:          9600,
		AutoReconnect:     true,
		ReconnectInterval: time.Hour,
	})

	c.handleIOError(errors.New("simulated I/O error"))

	c.mu.Lock()
	if c.reconnectTimer == nil {
		c.mu.Unlock()
		t.Fatal("expected a reconnect timer to be scheduled before Disconnect")
	}
	c.mu.Unlock()

	c.Disconnect()

	c.mu.Lock()
	timerCleared := c.reconnectTimer == nil
	stopped := c.stopped
	c.mu.Unlock()

	if !timerCleared {
		t.Fatal("expected reconnect timer to be cleared after Disconnect")
	}
	if !stopped {
		t.Fatal("expected stopped=true after Disconnect")
	}
}

func TestDisconnect_PreventsFutureReconnectScheduling(t *testing.T) {
	c := NewConnection(PortConfig{
		Path:              "COM_DOES_NOT_EXIST",
		BaudRate:          9600,
		AutoReconnect:     true,
		ReconnectInterval: time.Hour,
	})

	c.Disconnect()
	c.handleIOError(errors.New("simulated I/O error after stop"))

	c.mu.Lock()
	scheduled := c.reconnectTimer != nil
	c.mu.Unlock()

	if scheduled {
		t.Fatal("expected no reconnect scheduling once the connection has been stopped")
	}
}
