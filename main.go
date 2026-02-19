package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	fiberlogger "github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"

	"tms-backend/internal/database"
	"tms-backend/internal/handlers"
	"tms-backend/internal/services"
	"tms-backend/internal/tray"
	"tms-backend/internal/utils"
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

	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	// Initialize error logger
	if err := utils.InitLogger(); err != nil {
		log.Printf("Failed to initialize error logger: %v", err)
	}

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

	// Initialize Polling service (after MQTT is ready)
	log.Println("Initializing polling service...")
	services.GlobalPollingService = services.NewPollingService()

	// Initialize Fiber app
	fiberApp = fiber.New(fiber.Config{
		AppName:               "TMS Backend API",
		DisableStartupMessage: true,
	})

	// Middleware
	fiberApp.Use(fiberlogger.New())
	fiberApp.Use(cors.New())

	// Health check
	fiberApp.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// API routes
	api := fiberApp.Group("/api")

	// Device routes
	api.Get("/devices", handlers.GetDevices)
	api.Get("/devices/:id", handlers.GetDevice)
	api.Post("/devices", handlers.CreateDevice)
	api.Put("/devices/:id", handlers.UpdateDevice)
	api.Delete("/devices/:id", handlers.DeleteDevice)

	// Machine routes (legacy compatibility)
	api.Get("/machines", handlers.GetMachines)
	api.Put("/machines/:machineIp/:probeNo", handlers.UpdateMachine)

	// Temperature logs
	api.Get("/temp-logs", handlers.GetTempLogs)
	api.Get("/reports/templog", handlers.GetTempLogReport)

	// Temperature errors
	api.Get("/temp-errors", handlers.GetTempErrors)

	// Polling control
	api.Get("/poll", handlers.TriggerPoll)

	// SSE for real-time updates
	api.Get("/temperature-stream", handlers.TemperatureStream)

	// Start polling service
	log.Println("Starting polling service...")
	go services.GlobalPollingService.Start()

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Println("========================================")
	log.Printf("Starting TMS Backend Server on port %s", port)
	log.Println("========================================")

	tray.SetRunning()

	if err := fiberApp.Listen(":" + port); err != nil {
		utils.LogError("Failed to start server: %v", err)
		log.Printf("Failed to start server: %v", err)
		tray.SetError(fmt.Sprintf("Port %s in use", port))
	}
}

func cleanup() {
	log.Println("Shutting down gracefully...")
	if services.GlobalPollingService != nil {
		services.GlobalPollingService.Stop()
	}
	if services.GlobalMQTTService != nil {
		services.GlobalMQTTService.Disconnect()
	}
	if fiberApp != nil {
		fiberApp.Shutdown()
	}
	utils.CloseLogger()
}

func main() {
	// --- STARTUP DIAGNOSTIC ---
	// Write a debug file to diagnose startup issues
	// This runs before anything else so we can tell if the program starts at all
	startupDiag("[1/5] main() started")

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

	// Get port for tray tooltip
	if err := godotenv.Load(); err == nil {
		startupDiag("[4/5] .env loaded OK")
	} else {
		startupDiag(fmt.Sprintf("[WARN] .env not found: %v", err))
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	startupDiag(fmt.Sprintf("[5/5] Starting tray on port %s", port))

	// Run as system tray application
	tray.Run(port, startServer, cleanup)
}

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
	tmpLog := filepath.Join(tmpDir, "tms-backend-startup.log")
	if f, err := os.OpenFile(tmpLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		f.WriteString(line)
		f.Close()
	}

	// Also write next to the exe (may fail before changeToExeDir, that's OK)
	if f, err := os.OpenFile("startup_debug.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		f.WriteString(line)
		f.Close()
	}
}
