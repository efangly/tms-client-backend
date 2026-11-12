package utils

import (
	"net"
	"testing"
	"time"
)

func TestListenWithRetryWaitsForRelease(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := hold.Addr().String()
	go func() {
		time.Sleep(300 * time.Millisecond)
		hold.Close()
	}()

	ln, err := ListenWithRetry(addr, 5*time.Second, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("expected bind after release, got %v", err)
	}
	ln.Close()
}

func TestListenWithRetryTimesOut(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()

	start := time.Now()
	if _, err := ListenWithRetry(hold.Addr().String(), 300*time.Millisecond, 50*time.Millisecond); err == nil {
		t.Fatal("expected error while port is held")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("took too long to give up")
	}
}
