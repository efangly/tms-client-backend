package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	fiberlogger "github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"

	"tms-backend/internal/database"
	"tms-backend/internal/handlers"
	"tms-backend/internal/services"
	"tms-backend/internal/singleton"
	"tms-backend/internal/tray"
	"tms-backend/internal/updater"
	"tms-backend/internal/utils"
	"tms-backend/internal/version"
)

var fiberApp *fiber.App

// changeToExeDir changes the working directory to the executable's directory.
// This is critical for Windows Startup, where the working directory defaults to C:\Windows\System32.
func changeToExeDir() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %v", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %v", err)
	}
	exeDir := filepath.Dir(exePath)
	log.Printf("Working directory: %s", exeDir)
	log.Printf("TMS Backend version: %s", version.Version)
	return os.Chdir(exeDir)
}

func startServer() {
	// Recover from any panic
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC: %v", r)
			tray.SetError(fmt.Sprintf("PANIC: %v", r))
		}
	}()

	// .env is already loaded in main() before startServer is called.
	// Initialize error logger
	if err := utils.InitLogger(); err != nil {
		log.Printf("Failed to initialize error logger: %v", err)
	}

	// Start the updater first so a fix can still be installed when the
	// database or network below is failing.
	updater.Global.Start()

	// Wait for network to be ready (important for startup)
	log.Println("Waiting for network connectivity...")
	if !utils.WaitForNetwork(60 * time.Second) {
		utils.LogError("Network not ready after timeout")
		log.Println("WARNING: Network may not be ready, but continuing anyway...")
	}

	// Initialize database with retry
	log.Println("Connecting to database...")
	err := utils.RetryWithBackoff(
		"Database connection",
		func() error {
			return database.Connect()
		},
		5,              // max attempts
		2*time.Second,  // initial delay
		30*time.Second, // max delay
	)
	if err != nil {
		utils.LogError("Failed to connect to database after retries: %v", err)
		log.Printf("Failed to connect to database: %v", err)
		tray.SetError("Database connection failed")
		// The binary itself started fine; don't let a DB outage roll back
		// a freshly installed update.
		updater.MarkHealthy(version.Version)
		return
	}
	log.Println("Database connected successfully")

	// Initialize MQTT service with retry
	log.Println("Initializing MQTT service...")
	services.GlobalMQTTService = services.NewMQTTService()
	if services.GlobalMQTTService.IsEnabled() {
		err := utils.RetryWithBackoff(
			"MQTT connection",
			func() error {
				return services.GlobalMQTTService.Connect()
			},
			5,              // max attempts
			2*time.Second,  // initial delay
			30*time.Second, // max delay
		)
		if err != nil {
			utils.LogError("Failed to connect to MQTT broker after retries: %v", err)
			log.Printf("MQTT connection failed after retries: %v (continuing without MQTT)", err)
		}
	}

	// Initialize Serial service (persistent connections to USB-attached
	// probes, kept open from startup with auto-reconnect on drop)
	log.Println("Initializing serial service...")
	services.GlobalSerialService = services.NewSerialService()
	if err := services.GlobalSerialService.Start(); err != nil {
		log.Printf("Serial service start failed: %v (continuing without serial devices)", err)
	}

	// Initialize Polling service (after MQTT is ready)
	log.Println("Initializing polling service...")
	services.GlobalPollingService = services.NewPollingService()

	// Initialize Archive service (moves old temp_log rows to local files)
	services.GlobalArchiveService = services.NewArchiveService()

	// Initialize Fiber app
	fiberApp = fiber.New(fiber.Config{
		AppName:               "TMS Backend API",
		DisableStartupMessage: true,
	})

	// Middleware
	fiberApp.Use(fiberlogger.New())
	fiberApp.Use(cors.New())

	// Routes (including /health) — shared with tests via handlers.RegisterRoutes
	handlers.RegisterRoutes(fiberApp)

	// Once serving, confirm a just-installed update to the watchdog.
	fiberApp.Hooks().OnListen(func(fiber.ListenData) error {
		updater.MarkHealthy(version.Version)
		return nil
	})

	handlers.StartReportManager("reports")

	// Start polling service
	log.Println("Starting polling service...")
	go services.GlobalPollingService.Start()

	// Start archive service
	log.Println("Starting archive service...")
	go services.GlobalArchiveService.Start()

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Println("========================================")
	log.Printf("Starting TMS Backend Server on port %s", port)
	log.Println("========================================")

	tray.SetRunning()

	// Right after an update the previous process may still be releasing the
	// port, so retry the bind for a while before giving up.
	ln, err := utils.ListenWithRetry(":"+port, 60*time.Second, 500*time.Millisecond)
	if err == nil {
		err = fiberApp.Listener(ln)
	}
	if err != nil {
		utils.LogError("Failed to start server: %v", err)
		log.Printf("Failed to start server: %v", err)
		if holder := utils.DescribePortHolder(port); holder != "" {
			utils.LogError("%s", holder)
			log.Println(holder)
		}
		tray.SetError(fmt.Sprintf("Port %s in use", port))
		// The binary itself works; a busy port is environmental, so don't
		// roll back (and permanently skip) a freshly installed update.
		updater.MarkHealthy(version.Version)
	}
}

func cleanup() {
	log.Println("Shutting down gracefully...")

	// Force exit after timeout to prevent the process from hanging indefinitely
	// This is critical when running as Windows Startup - allows exe file to be updated
	go func() {
		time.Sleep(15 * time.Second)
		log.Println("Cleanup timed out after 15s, forcing exit")
		// Release the HTTP port before handing over, or the new exe can't bind.
		if fiberApp != nil {
			fiberApp.ShutdownWithTimeout(2 * time.Second)
		}
		spawnUpdateWatchdog()
		os.Exit(0)
	}()

	if updater.Global != nil {
		updater.Global.Stop()
	}
	if services.GlobalPollingService != nil {
		services.GlobalPollingService.Stop()
	}
	if services.GlobalArchiveService != nil {
		services.GlobalArchiveService.Stop()
	}
	handlers.StopReportManager()
	if services.GlobalSerialService != nil {
		services.GlobalSerialService.Stop()
	}
	if services.GlobalMQTTService != nil {
		services.GlobalMQTTService.Disconnect()
	}
	if fiberApp != nil {
		// ShutdownWithTimeout ensures SSE/long-poll connections are forced closed
		// so this call returns within ~5 s even when clients are still connected.
		if err := fiberApp.ShutdownWithTimeout(5 * time.Second); err != nil {
			log.Printf("Server shutdown timed out: %v", err)
		}
	}
	utils.CloseLogger()
	log.Println("Cleanup completed")
	// The HTTP port is free now, so the updated exe can start.
	spawnUpdateWatchdog()
	// Ensure the process always exits after cleanup, regardless of whether
	// systray's message loop returns cleanly.
	os.Exit(0)
}

var spawnWatchdogOnce sync.Once

// spawnUpdateWatchdog hands over to the watchdog (the previous exe), which
// starts the updated exe and rolls back if it fails. No-op unless the
// updater just swapped in a new version.
func spawnUpdateWatchdog() {
	if !updater.Global.RestartPending() {
		return
	}
	spawnWatchdogOnce.Do(func() {
		if err := updater.Global.SpawnWatchdog(); err != nil {
			utils.LogError("Failed to start update watchdog: %v", err)
			log.Printf("Failed to start update watchdog: %v", err)
		}
	})
}

// setupUpdater builds updater.Global from .env and wires it to the tray.
func setupUpdater() {
	cfg, err := updater.ConfigFromEnv()
	if err != nil {
		log.Printf("Updater config invalid, auto update disabled: %v", err)
		cfg.Enabled = false
	}
	cfg.Busy = func() bool {
		return handlers.ReportManager != nil && handlers.ReportManager.Busy()
	}
	cfg.Restart = tray.Quit
	cfg.OnStatus = func(st updater.Status) { tray.SetUpdateStatus(updateMenuText(st)) }
	updater.Global = updater.New(cfg, version.Version)

	st := updater.Global.Status()
	tray.SetUpdateStatus(updateMenuText(st))
	if st.State != updater.StateDisabled {
		tray.SetUpdater(updater.Global.CheckNow)
	}
}

func updateMenuText(st updater.Status) string {
	switch st.State {
	case updater.StateDisabled:
		return "Auto update disabled"
	case updater.StateChecking:
		return "Checking for updates..."
	case updater.StateUpToDate:
		return "Up to date - check again"
	case updater.StateDownloading:
		return "Downloading v" + st.Latest + "..."
	case updater.StateWaiting:
		return "Update v" + st.Latest + " ready (waiting)"
	case updater.StateApplying:
		return "Installing v" + st.Latest + "..."
	case updater.StateError:
		return "Update failed - retry"
	}
	return "Check for updates"
}

func main() {
	// Watchdog mode: launched from the previous exe after an update to
	// verify the new one (no tray, no server). See internal/updater.
	if len(os.Args) == 3 && os.Args[1] == updater.WatchdogFlag {
		updater.RunWatchdog(os.Args[2])
		return
	}

	// One instance per machine. After an update the old instance may still be
	// exiting, so wait for it before giving up.
	if !singleton.Acquire(`Global\TMSBackendServer`, 30*time.Second) {
		log.Println("Another TMS Backend instance is already running, exiting")
		startupDiag("[ERROR] another instance is running, exiting")
		return
	}

	// --- STARTUP DIAGNOSTIC ---
	// Write a debug file to diagnose startup issues
	// This runs before anything else so we can tell if the program starts at all
	startupDiag("[1/5] main() started")

	// Remember the directory the process was launched from before we chdir away.
	// `go run` compiles to a temp directory, so os.Executable() (used below) points
	// there instead of the project folder — this lets us fall back to the original
	// launch directory when looking for .env.
	originalDir, origDirErr := os.Getwd()

	// Change working directory to exe location (critical for Windows Startup)
	if err := changeToExeDir(); err != nil {
		startupDiag(fmt.Sprintf("[ERROR] changeToExeDir failed: %v", err))
		log.Printf("Warning: could not change to exe directory: %v", err)
	} else {
		startupDiag("[2/5] Working directory set OK")
	}

	// Initialize hidden console for log output (visible via tray menu)
	if err := tray.InitConsole(); err != nil {
		startupDiag(fmt.Sprintf("[WARN] InitConsole failed: %v", err))
		log.Printf("Warning: could not initialize console: %v", err)
	} else {
		startupDiag("[3/5] Console initialized OK")
	}

	// Get port for tray tooltip.
	// Try .env next to the exe first; if that's missing (e.g. `go run` chdir'd us into
	// a temp build dir), fall back to .env in the directory the process was launched from.
	if err := godotenv.Load(); err == nil {
		startupDiag("[4/5] .env loaded OK")
	} else if origDirErr == nil && originalDir != "" {
		fallbackEnv := filepath.Join(originalDir, ".env")
		if fallbackErr := godotenv.Load(fallbackEnv); fallbackErr == nil {
			startupDiag(fmt.Sprintf("[4/5] .env loaded OK (fallback: %s)", fallbackEnv))
		} else {
			startupDiag(fmt.Sprintf("[WARN] .env not found: %v (fallback %s also failed: %v)", err, fallbackEnv, fallbackErr))
		}
	} else {
		startupDiag(fmt.Sprintf("[WARN] .env not found: %v", err))
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	setupUpdater()

	startupDiag(fmt.Sprintf("[5/5] Starting tray on port %s", port))

	// Run as system tray application
	tray.Run(port, startServer, cleanup)
}

// maxStartupDiagLogSize caps each startup diagnostic log file so it doesn't
// grow forever across the many restarts of a long-lived background service.
const maxStartupDiagLogSize = 200 * 1024 // 200 KB

// startupDiag appends a diagnostic message to startup_debug.log.
// Writes to %TEMP% first (absolute, always writable) then also next to the exe.
// Used to debug startup issues; safe to leave in production builds.
func startupDiag(msg string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	line := timestamp + " " + msg + "\n"

	// Always write to %TEMP% first — guaranteed writable even from C:\Windows\System32
	tmpDir := os.Getenv("TEMP")
	if tmpDir == "" {
		tmpDir = os.Getenv("TMP")
	}
	if tmpDir == "" {
		tmpDir = `C:\Windows\Temp`
	}
	appendToDiagLog(filepath.Join(tmpDir, "tms-backend-startup.log"), line)

	// Also write next to the exe (may fail before changeToExeDir, that's OK)
	appendToDiagLog("startup_debug.log", line)
}

// appendToDiagLog appends line to path, truncating the file first if it has
// grown past maxStartupDiagLogSize so it stays bounded across restarts.
func appendToDiagLog(path, line string) {
	if info, err := os.Stat(path); err == nil && info.Size() > maxStartupDiagLogSize {
		os.Remove(path)
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		f.WriteString(line)
		f.Close()
	}
}
