package serialclient

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bug.st/serial"
)

// fakeScriptedPort is a serial.Port test double that returns a scripted
// sequence of read responses (one "frame" per Read call) and can be made to
// fail writes/reads on demand, without touching real hardware.
type fakeScriptedPort struct {
	mu            sync.Mutex
	writeErr      error
	writtenCmds   []string
	readErr       error
	readResponses [][]byte
	readIndex     int
}

func (p *fakeScriptedPort) SetMode(mode *serial.Mode) error { return nil }

func (p *fakeScriptedPort) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.writeErr != nil {
		return 0, p.writeErr
	}
	p.writtenCmds = append(p.writtenCmds, string(data))
	return len(data), nil
}

func (p *fakeScriptedPort) Read(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readErr != nil {
		return 0, p.readErr
	}
	if p.readIndex >= len(p.readResponses) {
		// No more scripted data: behave like a Read-timeout, matching
		// go.bug.st/serial's (0, nil) on timeout.
		return 0, nil
	}
	data := p.readResponses[p.readIndex]
	p.readIndex++
	n := copy(buf, data)
	return n, nil
}

func (p *fakeScriptedPort) Drain() error             { return nil }
func (p *fakeScriptedPort) ResetInputBuffer() error  { return nil }
func (p *fakeScriptedPort) ResetOutputBuffer() error { return nil }
func (p *fakeScriptedPort) SetDTR(bool) error        { return nil }
func (p *fakeScriptedPort) SetRTS(bool) error        { return nil }
func (p *fakeScriptedPort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return nil, nil
}
func (p *fakeScriptedPort) SetReadTimeout(time.Duration) error { return nil }
func (p *fakeScriptedPort) Close() error                       { return nil }
func (p *fakeScriptedPort) Break(time.Duration) error          { return nil }

func connectedTestConnection(port serial.Port, autoReconnect bool) *Connection {
	c := NewConnection(PortConfig{
		Path:              "COM_TEST",
		BaudRate:          9600,
		AutoReconnect:     autoReconnect,
		ReconnectInterval: time.Hour,
	})
	c.mu.Lock()
	c.connected = true
	c.port = port
	c.mu.Unlock()
	return c
}

// nineByteFrame is the standard single-probe frame also used in
// tcpclient_test.go: raw 0x19A3 = 6563 -> temp = (6563-4000)*0.01 = 25.63°C.
var nineByteFrame = []byte{0x41, 0x41, 0x5a, 0x00, 0x5a, 0x19, 0xa3, 0x5a, 0x0d}

// brokenSensorFrame carries raw 0xFFFF, which parses to temp=615.35°C — well
// outside tcpclient's valid range, so FilterValidProbes drops it and Request
// should treat it as an anomaly worth retrying.
var brokenSensorFrame = []byte{0x41, 0x41, 0x5a, 0x00, 0x5a, 0xFF, 0xFF, 0x5a, 0x0d}

func TestRequest_SingleProbeSuccess(t *testing.T) {
	fake := &fakeScriptedPort{readResponses: [][]byte{nineByteFrame}}
	conn := connectedTestConnection(fake, false)

	resp := conn.Request("A", 100*time.Millisecond)

	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if !resp.Connected {
		t.Fatal("expected Connected=true")
	}
	if len(resp.Probes) != 1 {
		t.Fatalf("expected 1 probe, got %d", len(resp.Probes))
	}
	if resp.Probes[0].RealValue != 6563 {
		t.Errorf("RealValue = %d, want 6563", resp.Probes[0].RealValue)
	}
	if resp.Probes[0].TempValue != 25.63 {
		t.Errorf("TempValue = %v, want 25.63", resp.Probes[0].TempValue)
	}
	if len(fake.writtenCmds) != 1 || fake.writtenCmds[0] != "A\r" {
		t.Errorf("written commands = %v, want exactly one \"A\\r\"", fake.writtenCmds)
	}
}

func TestRequest_NotConnectedReturnsError(t *testing.T) {
	c := NewConnection(PortConfig{Path: "COM_TEST", BaudRate: 9600})

	resp := c.Request("A", 100*time.Millisecond)

	if resp.Error == "" {
		t.Fatal("expected an error when the connection has no open port")
	}
	if resp.Connected {
		t.Fatal("expected Connected=false")
	}
}

func TestRequest_WriteErrorMarksDisconnected(t *testing.T) {
	fake := &fakeScriptedPort{writeErr: errors.New("simulated write failure")}
	conn := connectedTestConnection(fake, false)

	resp := conn.Request("A", 100*time.Millisecond)

	if resp.Error == "" || !strings.Contains(resp.Error, "simulated write failure") {
		t.Fatalf("expected write error surfaced, got %q", resp.Error)
	}
	if conn.IsConnected() {
		t.Fatal("expected connection to be marked disconnected after a write error")
	}
}

func TestRequest_ReadErrorMarksDisconnected(t *testing.T) {
	fake := &fakeScriptedPort{readErr: errors.New("simulated read failure")}
	conn := connectedTestConnection(fake, false)

	resp := conn.Request("A", 100*time.Millisecond)

	if resp.Error == "" || !strings.Contains(resp.Error, "simulated read failure") {
		t.Fatalf("expected read error surfaced, got %q", resp.Error)
	}
	if conn.IsConnected() {
		t.Fatal("expected connection to be marked disconnected after a read error")
	}
}

func TestRequest_RetriesOnAnomalousReading(t *testing.T) {
	fake := &fakeScriptedPort{readResponses: [][]byte{
		brokenSensorFrame, // attempt 1: anomalous, retry
		brokenSensorFrame, // attempt 2: anomalous, retry
		nineByteFrame,     // attempt 3: valid, accepted
	}}
	conn := connectedTestConnection(fake, false)

	resp := conn.Request("A", 100*time.Millisecond)

	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if len(fake.writtenCmds) != 3 {
		t.Fatalf("expected 3 send attempts, got %d", len(fake.writtenCmds))
	}
	if len(resp.Probes) != 1 || resp.Probes[0].RealValue != 6563 {
		t.Fatalf("expected the final valid probe (RealValue=6563), got %+v", resp.Probes)
	}
}

func TestRequest_DefaultsCommandToA(t *testing.T) {
	fake := &fakeScriptedPort{readResponses: [][]byte{nineByteFrame}}
	conn := connectedTestConnection(fake, false)

	conn.Request("", 100*time.Millisecond)

	if len(fake.writtenCmds) != 1 || fake.writtenCmds[0] != "A\r" {
		t.Errorf("written commands = %v, want default command \"A\\r\"", fake.writtenCmds)
	}
}
