package serialclient

import (
	"strings"
	"sync"
)

// IsSerialPath reports whether a machine_ip value refers to a USB serial
// port (e.g. "COM3") rather than a network address. Devices are wired up as
// serial when their configured IP starts with "COM" (case-insensitive).
func IsSerialPath(machineIP string) bool {
	return strings.HasPrefix(strings.ToUpper(machineIP), "COM")
}

// Manager holds one persistent Connection per serial port path, so callers
// can look up an already-open connection instead of dialing per request.
type Manager struct {
	mu          sync.Mutex
	connections map[string]*Connection
}

// NewManager creates an empty Manager.
func NewManager() *Manager {
	return &Manager{connections: make(map[string]*Connection)}
}

// Open returns the existing Connection for config.Path, or creates and
// tracks a new one if this is the first time the path has been seen. It
// does not call Connect — the caller decides when to open the port.
func (m *Manager) Open(config PortConfig) *Connection {
	m.mu.Lock()
	defer m.mu.Unlock()

	if conn, ok := m.connections[config.Path]; ok {
		return conn
	}
	conn := NewConnection(config)
	m.connections[config.Path] = conn
	return conn
}

// Get returns the tracked Connection for path, if any.
func (m *Manager) Get(path string) (*Connection, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	conn, ok := m.connections[path]
	return conn, ok
}

// StopAll disconnects every tracked connection, e.g. during app shutdown.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, conn := range m.connections {
		conn.Disconnect()
	}
}
