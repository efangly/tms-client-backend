//go:build e2e

package e2e

import (
	"net"
	"sync"
)

// fakeProbeServer stands in for a physical temperature probe. It speaks the
// real single-probe 9-byte wire protocol parsed by
// internal/tcpclient/tcpclient.go's parseHexResponse (header 41 41, 5A
// separators, 0x0D terminator), so the polling service's real TCP client
// code path is exercised end-to-end without physical hardware.
//
// Frame: 41 41 5A 00 5A <hi> <lo> 5A 0D, where raw = (hi<<8|lo) and
// temp = (raw-4000)*0.01 — the inverse of tcpclient's parsing formula.
type fakeProbeServer struct {
	addr string
	ln   net.Listener

	mu   sync.Mutex
	temp float64
}

func newFakeProbeServer(addr string) *fakeProbeServer {
	return &fakeProbeServer{addr: addr, temp: 22.5}
}

func (f *fakeProbeServer) Start() error {
	ln, err := net.Listen("tcp", f.addr)
	if err != nil {
		return err
	}
	f.ln = ln
	go f.acceptLoop()
	return nil
}

func (f *fakeProbeServer) Stop() {
	if f.ln != nil {
		f.ln.Close()
	}
}

// SetTemp changes the temperature (°C) reported to the next poll request.
func (f *fakeProbeServer) SetTemp(celsius float64) {
	f.mu.Lock()
	f.temp = celsius
	f.mu.Unlock()
}

func (f *fakeProbeServer) acceptLoop() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go f.handle(conn)
	}
}

func (f *fakeProbeServer) handle(conn net.Conn) {
	defer conn.Close()

	// The real client writes "<command>\r" and reads until it sees 0x0D;
	// we don't care about the command, just that a request arrived.
	buf := make([]byte, 64)
	if _, err := conn.Read(buf); err != nil {
		return
	}

	f.mu.Lock()
	temp := f.temp
	f.mu.Unlock()

	raw := uint16(temp/0.01 + 4000)
	frame := []byte{0x41, 0x41, 0x5a, 0x00, 0x5a, byte(raw >> 8), byte(raw & 0xff), 0x5a, 0x0d}
	conn.Write(frame)
}
