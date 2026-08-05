package services

import (
	"testing"
	"time"

	"tms-backend/internal/serialclient"
)

// withGlobalSerialService temporarily swaps GlobalSerialService for the
// duration of a test and restores it afterward.
func withGlobalSerialService(t *testing.T, s *SerialService) {
	t.Helper()
	prev := GlobalSerialService
	GlobalSerialService = s
	t.Cleanup(func() { GlobalSerialService = prev })
}

func TestRequestProbes_ComPath_NoGlobalSerialService(t *testing.T) {
	withGlobalSerialService(t, nil)

	probes := requestProbes("COM3", "A", 100*time.Millisecond)

	if probes != nil {
		t.Errorf("expected nil probes when GlobalSerialService is not initialized, got %+v", probes)
	}
}

func TestRequestProbes_ComPath_UnknownPort(t *testing.T) {
	withGlobalSerialService(t, NewSerialService())

	probes := requestProbes("COM9", "A", 100*time.Millisecond)

	if len(probes) != 0 {
		t.Errorf("expected no probes for a COM path with no tracked connection, got %+v", probes)
	}
}

func TestRequestProbes_ComPath_TrackedButNotConnected(t *testing.T) {
	s := NewSerialService()
	// Register the port without opening it (mirrors a device that hasn't
	// connected yet, or a Connect() failure at startup).
	s.manager.Open(serialclient.PortConfig{Path: "COM3", BaudRate: 9600})
	withGlobalSerialService(t, s)

	probes := requestProbes("COM3", "A", 100*time.Millisecond)

	if len(probes) != 0 {
		t.Errorf("expected no probes from an unconnected port, got %+v", probes)
	}
}

func TestRequestProbes_NonComPath_DoesNotUseSerialService(t *testing.T) {
	withGlobalSerialService(t, nil) // would panic/nil-deref if the serial branch were mistakenly taken

	// 127.0.0.1 refuses connections instantly on an unused port, so this
	// exercises the TCP branch without hanging for the full timeout.
	probes := requestProbes("127.0.0.1", "A", 200*time.Millisecond)

	if len(probes) != 0 {
		t.Errorf("expected no probes from a failed TCP dial, got %+v", probes)
	}
}
