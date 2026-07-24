//go:build e2e

// Package e2e is a black-box test suite that boots the real Fiber app on a
// real TCP listener, backed by a real MySQL database (a dedicated e2e schema
// on the shared dev host — see .env.e2e.example), and drives it purely over
// HTTP. It is excluded from a plain `go test ./...` run via the "e2e" build
// tag: run it explicitly with `go test -tags=e2e ./e2e/...`.
//
// TCP probe devices don't exist on this network, so polling/alert workflows
// are exercised against fakeProbeServer (see fake_probe.go), which speaks
// the real wire protocol from internal/tcpclient/tcpclient.go.
package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"

	"tms-backend/internal/database"
	"tms-backend/internal/handlers"
	"tms-backend/internal/models"
	"tms-backend/internal/services"
)

var (
	baseURL    string
	httpClient = &http.Client{Timeout: 10 * time.Second}
	probe      *fakeProbeServer
)

func TestMain(m *testing.M) {
	if err := godotenv.Load(".env.e2e"); err != nil {
		log.Fatalf("e2e: failed to load .env.e2e (copy .env.e2e.example to .env.e2e and fill in real credentials): %v", err)
	}

	if err := database.Connect(); err != nil {
		log.Fatalf("e2e: failed to connect to database: %v", err)
	}
	if err := database.DB.AutoMigrate(
		&models.MasterMachine{},
		&models.TempLog{},
		&models.TempError{},
		&models.TempLogArchive{},
		&models.ArchiveManifest{},
	); err != nil {
		log.Fatalf("e2e: failed to migrate schema: %v", err)
	}

	// No system tray, no MQTT (unset in .env.e2e), no automatic ticking —
	// tests trigger polling/archiving explicitly via their HTTP endpoints.
	services.GlobalMQTTService = nil
	services.GlobalPollingService = services.NewPollingService()
	services.GlobalArchiveService = services.NewArchiveService()

	// ARCHIVE_DIR/DEFAULT_TCP_PORT are read from the OS environment in each
	// package's init(), which runs before the godotenv.Load above — so an
	// ARCHIVE_DIR set only in .env.e2e never takes effect in-process here.
	// Ask the packages what they actually ended up using instead of
	// re-reading (and trusting) the env ourselves.
	archiveDir := services.ArchiveDir()
	os.RemoveAll(archiveDir)
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		log.Fatalf("e2e: failed to create archive dir: %v", err)
	}

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	handlers.RegisterRoutes(app)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("e2e: failed to open listener: %v", err)
	}
	baseURL = "http://" + ln.Addr().String()
	go func() {
		if err := app.Listener(ln); err != nil {
			log.Printf("e2e: server stopped: %v", err)
		}
	}()

	if err := waitForHealth(10 * time.Second); err != nil {
		log.Fatalf("e2e: server never became healthy: %v", err)
	}

	// Listen on 0.0.0.0 (not just 127.0.0.1) so tests can use distinct
	// loopback addresses (127.0.0.1, 127.0.0.2, ...) as separate "machine IP"
	// identities that all reach the same fake device, keeping each test's
	// alert-state-machine key independent of run order.
	tcpPort := strconv.Itoa(services.DefaultTCPPort())
	probe = newFakeProbeServer("0.0.0.0:" + tcpPort)
	if err := probe.Start(); err != nil {
		log.Fatalf("e2e: failed to start fake probe server: %v", err)
	}

	code := m.Run()

	probe.Stop()
	_ = app.ShutdownWithTimeout(2 * time.Second)
	os.RemoveAll(archiveDir)
	os.Exit(code)
}

func waitForHealth(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return lastErr
}

// truncateAll clears every table the e2e suite touches so each test starts
// from a clean slate — including leftover master_machine rows, which would
// otherwise slow down (or corrupt) polling tests in later files.
func truncateAll(t *testing.T) {
	t.Helper()
	for _, tbl := range []string{"temp_error", "temp_log_archive", "temp_log", "archive_manifest", "master_machine"} {
		if err := database.DB.Exec("DELETE FROM " + tbl).Error; err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}

// doRequest performs a real HTTP round trip against the running e2e server
// and returns the response (body already drained/closed) plus its raw bytes.
func doRequest(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body for %s %s: %v", method, path, err)
	}
	return resp, data
}

// createDevice is a small helper wrapping POST /api/devices for tests that
// just need a device to exist and don't care about the response.
func createDevice(t *testing.T, device map[string]any) {
	t.Helper()
	resp, body := doRequest(t, http.MethodPost, "/api/devices", device)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("createDevice: status = %d, body = %s", resp.StatusCode, body)
	}
}

// waitFor polls cond until it returns true or timeout elapses, failing the
// test otherwise. Used for the async paths (TriggerPoll runs in a goroutine).
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}
