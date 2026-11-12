package utils

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"time"
)

// ListenWithRetry binds a TCP listener on addr, retrying until timeout.
// After an auto update the previous process (or a leftover instance) may
// still be releasing the port for a moment, so a single failed bind should
// not be fatal.
func ListenWithRetry(addr string, timeout, interval time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(timeout)
	for attempt := 1; ; attempt++ {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		if time.Now().Add(interval).After(deadline) {
			return nil, err
		}
		if attempt == 1 || attempt%20 == 0 {
			log.Printf("Port %s busy (%v), retrying...", addr, err)
		}
		time.Sleep(interval)
	}
}

// DescribePortHolder returns the netstat lines for sockets using the given
// port, to identify who is holding it. Best effort; empty if unavailable.
func DescribePortHolder(port string) string {
	out, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, ":"+port+" ") {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("netstat entries for port %s:\n%s", port, strings.Join(lines, "\n"))
}
